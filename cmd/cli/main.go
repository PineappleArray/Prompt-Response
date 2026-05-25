package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	url := flag.String("url", "http://localhost:8080/v1/chat/completions", "router chat-completions URL")
	model := flag.String("model", "auto", "model name to request (the router routes it)")
	system := flag.String("system", "", "optional system prompt sent as the first message")
	flag.Parse()

	p := tea.NewProgram(newModel(*url, *model, *system), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
