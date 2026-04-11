package classifier

import "testing"

func BenchmarkClassify_Simple(b *testing.B) {
	c := newTestClassifier()
	req := Request{UserMessage: "what is 2+2", TokenCount: 5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Classify(req)
	}
}

func BenchmarkClassify_Complex(b *testing.B) {
	c := newTestClassifier()
	req := Request{
		SystemPrompt: "You are a senior software engineer",
		UserMessage:  "implement a distributed consensus algorithm with edge case handling step by step, explain the tradeoffs of each approach at production scale",
		TokenCount:   150,
		HasCode:      true,
		ConvTurns:    5,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Classify(req)
	}
}
