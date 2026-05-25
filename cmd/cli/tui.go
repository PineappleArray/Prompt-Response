package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles. Subtle borders so the layout reads as a split pane.
var (
	codePaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Padding(0, 1)
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39"))
)

// Bubbletea messages we send to ourselves.
type sendStartedMsg struct {
	ch     <-chan StreamMsg
	cancel context.CancelFunc
	err    error
}
type streamEventMsg struct {
	delta string
	err   error
	done  bool
}

type model struct {
	url, modelID, system string

	// Conversation state: every Enter appends to msgs and the full list
	// is POSTed so the router sees a stable first user message and pins
	// the conversation.
	msgs []Message

	input    string
	output   strings.Builder
	status   string
	width    int
	height   int
	streamCh <-chan StreamMsg
	cancel   context.CancelFunc
	started  time.Time
	lastMS   int64
}

func newModel(url, modelID, system string) *model {
	m := &model{
		url:     url,
		modelID: modelID,
		system:  system,
		status:  "ready — type a prompt and press Enter (Esc to quit)",
	}
	if system != "" {
		m.msgs = append(m.msgs, Message{Role: "system", Content: system})
	}
	return m
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(raw tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := raw.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		// While streaming, only Esc/Ctrl+C are honored — they cancel.
		if m.streamCh != nil {
			switch msg.Type {
			case tea.KeyEsc, tea.KeyCtrlC:
				if m.cancel != nil {
					m.cancel()
				}
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			if strings.TrimSpace(m.input) == "" {
				return m, nil
			}
			m.msgs = append(m.msgs, Message{Role: "user", Content: m.input})
			m.appendOutput(fmt.Sprintf("\n> %s\n\n", m.input))
			m.input = ""
			m.status = "sending..."
			m.started = time.Now()
			return m, startSend(m.url, m.modelID, m.msgs)
		case tea.KeyBackspace, tea.KeyCtrlH:
			if n := len(m.input); n > 0 {
				m.input = m.input[:n-1]
			}
		case tea.KeyRunes, tea.KeySpace:
			m.input += string(msg.Runes)
		}
		return m, nil

	case sendStartedMsg:
		if msg.err != nil {
			m.appendOutput(fmt.Sprintf("[error] %v\n", msg.err))
			m.status = "ready"
			return m, nil
		}
		m.streamCh = msg.ch
		m.cancel = msg.cancel
		m.status = "streaming... (Esc to cancel)"
		return m, waitForStream(msg.ch)

	case streamEventMsg:
		if msg.done {
			// Persist the assistant message so multi-turn context is sent
			// next time. The router's conversation pin needs this to stay
			// stable.
			content := m.lastAssistantContent()
			if content != "" {
				m.msgs = append(m.msgs, Message{Role: "assistant", Content: content})
			}
			if m.cancel != nil {
				m.cancel()
				m.cancel = nil
			}
			m.streamCh = nil
			m.lastMS = time.Since(m.started).Milliseconds()
			m.status = "ready"
			return m, nil
		}
		if msg.err != nil {
			m.appendOutput(fmt.Sprintf("\n[stream error] %v\n", msg.err))
			return m, waitForStream(m.streamCh)
		}
		m.appendOutput(msg.delta)
		return m, waitForStream(m.streamCh)
	}
	return m, nil
}

// lastAssistantContent extracts the trailing assistant turn from the
// rendered output buffer. The rendering writes `> user\n\n<reply>\n`
// segments; everything after the final `\n\n` is the latest assistant
// response.
func (m *model) lastAssistantContent() string {
	out := m.output.String()
	idx := strings.LastIndex(out, "\n\n")
	if idx < 0 {
		return strings.TrimSpace(out)
	}
	return strings.TrimSpace(out[idx+2:])
}

func (m *model) appendOutput(s string) { m.output.WriteString(s) }

func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	statusLine := fmt.Sprintf("model=%s  url=%s  last=%dms  %s",
		m.modelID, m.url, m.lastMS, m.status)

	// Reserve 3 lines for status + input border + input line + bottom border.
	inputBox := inputStyle.Width(m.width - 2).Render(promptStyle.Render("> ") + m.input)
	codeHeight := m.height - lipgloss.Height(inputBox) - 1
	if codeHeight < 3 {
		codeHeight = 3
	}

	codeBody := lastNLines(m.output.String(), codeHeight-2)
	codePane := codePaneStyle.Width(m.width - 2).Height(codeHeight).Render(codeBody)
	status := statusStyle.Width(m.width).Render(statusLine)

	return lipgloss.JoinVertical(lipgloss.Left, codePane, status, inputBox)
}

// lastNLines returns the trailing n lines of s, padding with blanks
// when there are fewer.
func lastNLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func startSend(url, modelID string, msgs []Message) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := Send(ctx, url, modelID, msgs)
		if err != nil {
			cancel()
			return sendStartedMsg{err: err}
		}
		return sendStartedMsg{ch: ch, cancel: cancel}
	}
}

func waitForStream(ch <-chan StreamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamEventMsg{done: true}
		}
		return streamEventMsg{delta: msg.Delta, err: msg.Err}
	}
}
