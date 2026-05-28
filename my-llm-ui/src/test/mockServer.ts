import express from "express";
import cors from "cors";

const app = express();
app.use(cors());
app.use(express.json());

const RESPONSES: Record<string, string> = {
  opus: "I'm Claude Opus, the most capable model. Let me provide a thorough and detailed response to your question. I'll analyze this from multiple angles to give you the most comprehensive answer possible.\n\nFirst, let's consider the key factors at play here. There are several important dimensions to think about, and I want to make sure we cover each one carefully.\n\nIn conclusion, I hope this detailed analysis helps clarify things for you.",
  sonnet: "Here's a balanced take on your question. I'll aim to be both thorough and concise.\n\nThe key points to consider are the trade-offs involved. Each approach has its merits, and the right choice depends on your specific context.\n\nLet me know if you'd like me to dig deeper into any of these areas.",
  haiku: "Quick answer: yes, that's correct. The main thing to know is that it depends on your use case. Happy to elaborate if needed.",
};

// POST /v1/chat/completions — OpenAI-compatible SSE stream
// This mimics exactly what your Go backend proxies from the upstream replica.
app.post("/v1/chat/completions", (req, res) => {
  const { model, messages, stream } = req.body;

  if (!messages || messages.length === 0) {
    res.status(400).json({ error: { message: "messages required", type: "invalid_request" } });
    return;
  }

  const lastUser = messages.filter((m: any) => m.role === "user").pop();
  console.log(`[stream] model=${model} stream=${stream} message="${lastUser?.content?.slice(0, 50)}..."`);

  // Non-streaming request — return a single response
  if (!stream) {
    const fullText = RESPONSES[model] ?? RESPONSES.sonnet;
    res.json({
      id: `chatcmpl-${Date.now()}`,
      object: "chat.completion",
      created: Math.floor(Date.now() / 1000),
      model: model ?? "test-model",
      choices: [{ index: 0, message: { role: "assistant", content: fullText }, finish_reason: "stop" }],
      usage: { prompt_tokens: 10, completion_tokens: fullText.length / 4 },
    });
    return;
  }

  // Streaming — SSE in OpenAI format (same as what your Go backend proxies)
  const fullText = RESPONSES[model] ?? RESPONSES.sonnet;

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.setHeader("X-Accel-Buffering", "no");
  res.flushHeaders();

  const words = fullText.split(/(\s+)/);
  let i = 0;

  const interval = setInterval(() => {
    if (i >= words.length) {
      // Send [DONE] sentinel — same as OpenAI and your streamInterceptor expects
      res.write(`data: [DONE]\n\n`);
      clearInterval(interval);
      res.end();
      console.log(`[stream] done, sent ${words.length} chunks`);
      return;
    }

    const chunkSize = Math.min(Math.floor(Math.random() * 3) + 1, words.length - i);
    const content = words.slice(i, i + chunkSize).join("");
    i += chunkSize;

    // OpenAI-compatible chunk format — identical to what replicas send
    const chunk = {
      id: `chatcmpl-test-${Date.now()}`,
      object: "chat.completion.chunk",
      created: Math.floor(Date.now() / 1000),
      model: model ?? "test-model",
      choices: [
        {
          index: 0,
          delta: { content },
          finish_reason: null,
        },
      ],
    };

    res.write(`data: ${JSON.stringify(chunk)}\n\n`);
  }, 50);

  req.on("close", () => {
    clearInterval(interval);
    console.log("[stream] client disconnected");
  });
});

app.get("/healthz", (_req, res) => {
  res.json({ status: "ok" });
});

const PORT = 8080;
app.listen(PORT, () => {
  console.log(`Mock server on http://localhost:${PORT}`);
  console.log(`  POST /v1/chat/completions  — OpenAI-compatible SSE`);
  console.log(`  GET  /healthz              — health check`);
});