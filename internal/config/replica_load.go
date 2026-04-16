package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// ---- Types ----

type vLLMServer struct {
	cmd   *exec.Cmd
	model string
	url   string
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

type testCase struct {
	name   string
	prompt string
	check  func(response string) error
}

// ---- Server lifecycle ----

func startVLLM(t *testing.T, model string, port int) *vLLMServer {
	t.Helper()

	cmd := exec.Command("vllm", "serve", model,
		"--port", fmt.Sprintf("%d", port),
		"--gpu-memory-utilization", "0.85",
	)
	// Setpgid so we can kill the whole process group on teardown.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start vLLM for %s: %v", model, err)
	}

	srv := &vLLMServer{
		cmd:   cmd,
		model: model,
		url:   fmt.Sprintf("http://localhost:%d", port),
	}

	if err := srv.waitReady(2 * time.Minute); err != nil {
		srv.stop(t)
		t.Fatalf("vLLM did not become ready for %s: %v", model, err)
	}
	return srv
}

func (s *vLLMServer) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.url + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s", s.url)
}

func (s *vLLMServer) stop(t *testing.T) {
	t.Helper()
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	// Kill the process group so child processes (NCCL, workers) also die.
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Logf("vLLM for %s did not exit cleanly; forcing kill", s.model)
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}

	// Give the CUDA allocator a moment to actually release VRAM
	// before the next model tries to allocate.
	time.Sleep(5 * time.Second)
}

// ---- Inference ----

func (s *vLLMServer) chat(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:    s.model,
		Messages: []message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		s.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}

// ---- The test ----

func TestModelsAcrossTestCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping model load/unload test in -short mode")
	}

	models := []string{
		"Qwen/Qwen2.5-0.5B-Instruct",
		"facebook/opt-125m",
	}

	cases := []testCase{
		{
			name:   "non_empty_response",
			prompt: "Say hello.",
			check: func(r string) error {
				if len(r) == 0 {
					return fmt.Errorf("empty response")
				}
				return nil
			},
		},
		{
			name:   "answers_simple_math",
			prompt: "What is 2 + 2? Reply with just the number.",
			check: func(r string) error {
				if !bytes.Contains([]byte(r), []byte("4")) {
					return fmt.Errorf("expected '4' in response, got: %q", r)
				}
				return nil
			},
		},
	}

	for _, model := range models {
		model := model
		t.Run(model, func(t *testing.T) {
			srv := startVLLM(t, model, 8000)
			t.Cleanup(func() { srv.stop(t) })

			for _, tc := range cases {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()

					resp, err := srv.chat(ctx, tc.prompt)
					if err != nil {
						t.Fatalf("chat failed: %v", err)
					}
					if err := tc.check(resp); err != nil {
						t.Errorf("check failed: %v\nresponse: %s", err, resp)
					}
				})
			}
		})
	}
}
