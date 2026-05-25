package main

import (
	"strings"
	"testing"
)

func TestParseSSEStream_HappyPath(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`, "",
		`data: {"choices":[{"delta":{"content":", "}}]}`, "",
		`data: {"choices":[{"delta":{"content":"world!"}}]}`, "",
		`data: [DONE]`, "",
	}, "\n")

	var got []string
	parseSSEStream(strings.NewReader(stream),
		func(s string) { got = append(got, s) },
		func(err error) { t.Fatalf("unexpected scanner err: %v", err) },
	)

	want := []string{"Hello", ", ", "world!"}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("deltas = %v, want %v", got, want)
	}
}

func TestParseSSEStream_IgnoresNonDataAndBadJSON(t *testing.T) {
	stream := strings.Join([]string{
		`event: ping`, "",
		`data: {"choices":[{"delta":{"content":"only"}}]}`, "",
		`data: not-json`, "",
		`data: {"choices":[]}`, "",
		`data: [DONE]`, "",
	}, "\n")

	var got []string
	parseSSEStream(strings.NewReader(stream),
		func(s string) { got = append(got, s) },
		func(err error) { t.Fatalf("unexpected scanner err: %v", err) },
	)

	if len(got) != 1 || got[0] != "only" {
		t.Errorf("expected exactly [\"only\"], got %v", got)
	}
}

func TestParseSSEStream_StopsAtDONE(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"a"}}]}`, "",
		`data: [DONE]`, "",
		`data: {"choices":[{"delta":{"content":"after"}}]}`, "",
	}, "\n")

	var got []string
	parseSSEStream(strings.NewReader(stream),
		func(s string) { got = append(got, s) },
		func(err error) { t.Fatalf("unexpected scanner err: %v", err) },
	)

	if len(got) != 1 || got[0] != "a" {
		t.Errorf("expected stream to stop at [DONE]; got %v", got)
	}
}
