import { useRef, useEffect } from "react";
import { useChat } from "./useChat";
import { fontStack } from "./theme";

interface MessageBubbleProps {
  role: string;
  content: string;
  isStreaming: boolean;
}

// ── Components ─────────────────────────────────────────────────────
function TypingIndicator() {
  return (
    <div style={{ display: "flex", gap: 4, padding: "8px 0", alignItems: "center" }}>
      {[0, 1, 2].map((i) => (
        <div
          key={i}
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "var(--text-muted)",
            animation: `dotPulse 1.2s ease-in-out ${i * 0.2}s infinite`,
          }}
        />
      ))}
    </div>
  );
}

function MessageBubble({ role, content, isStreaming }: MessageBubbleProps) {
  const isUser = role === "user";
  return (
    <div
      style={{
        display: "flex",
        justifyContent: isUser ? "flex-end" : "flex-start",
        marginBottom: 16,
        animation: "fadeSlideUp 0.3s ease-out",
      }}
    >
      <div
        style={{
          maxWidth: isUser ? "75%" : "85%",
          display: "flex",
          gap: 12,
          flexDirection: isUser ? "row-reverse" : "row",
          alignItems: "flex-start",
        }}
      >
        {!isUser && (
          <div
            style={{
              width: 28,
              height: 28,
              borderRadius: 8,
              background: "var(--accent)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              flexShrink: 0,
              marginTop: 2,
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
              <path
                d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"
                stroke="white"
                strokeWidth="2"
                strokeLinejoin="round"
                strokeLinecap="round"
              />
            </svg>
          </div>
        )}
        <div
          style={{
            padding: isUser ? "10px 16px" : "2px 0",
            borderRadius: isUser ? 20 : 0,
            background: isUser ? "var(--user-bubble)" : "transparent",
            color: "var(--text-primary)",
            fontSize: 15,
            lineHeight: 1.65,
            letterSpacing: "-0.01em",
            fontFamily: fontStack,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {content}
          {isStreaming && (
            <span
              style={{
                display: "inline-block",
                width: 2,
                height: 18,
                background: "var(--text-primary)",
                marginLeft: 2,
                verticalAlign: "text-bottom",
                animation: "cursorBlink 0.8s step-end infinite",
              }}
            />
          )}
        </div>
      </div>
    </div>
  );
}

// ── Chat body ──────────────────────────────────────────────────────
// Renders the message log and the input box. The top bar (nav + model
// selector) is owned by App.tsx; this component receives the selected model id.
export default function ChatView({ selectedModel }: { selectedModel: string }) {
  // Point this at the Go backend's stream endpoint (Vite proxies /v1 → :8080).
  const { messages, input, isStreaming, error, send, stop, setInput, clearError } =
    useChat("/v1/chat/completions");

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleSend = () => {
    if (!input.trim() || isStreaming) return;
    send(selectedModel);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>): void => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const lastMsg = messages[messages.length - 1];
  const isLastAssistantStreaming = isStreaming && lastMsg?.role === "assistant";

  return (
    <>
      {/* Messages Area */}
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "24px 16px",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ maxWidth: 680, width: "100%", margin: "0 auto", flex: 1 }}>
          {messages.length === 0 && !isStreaming && (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                justifyContent: "center",
                height: "100%",
                gap: 8,
                opacity: 0.5,
              }}
            >
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none">
                <path
                  d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"
                  stroke="var(--text-muted)"
                  strokeWidth="1.5"
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
              </svg>
              <span style={{ fontSize: 16, color: "var(--text-muted)", fontWeight: 500 }}>
                How can I help you today?
              </span>
            </div>
          )}

          {messages.map((msg, i) => {
            const isLastAssistant = i === messages.length - 1 && msg.role === "assistant";
            return (
              <MessageBubble
                key={i}
                role={msg.role}
                content={msg.content}
                isStreaming={isLastAssistant && isLastAssistantStreaming}
              />
            );
          })}

          {isStreaming && lastMsg?.role === "assistant" && lastMsg.content === "" && (
            <div style={{ display: "flex", gap: 12, alignItems: "flex-start", marginBottom: 16 }}>
              <div
                style={{
                  width: 28,
                  height: 28,
                  borderRadius: 8,
                  background: "var(--accent)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  flexShrink: 0,
                }}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
                  <path
                    d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"
                    stroke="white"
                    strokeWidth="2"
                    strokeLinejoin="round"
                    strokeLinecap="round"
                  />
                </svg>
              </div>
              <TypingIndicator />
            </div>
          )}

          {error && (
            <div
              style={{
                padding: "10px 16px",
                borderRadius: 12,
                background: "#f5e6e1",
                color: "#8b3a2a",
                fontSize: 14,
                marginBottom: 16,
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
              }}
            >
              <span>{error}</span>
              <button
                onClick={clearError}
                style={{
                  border: "none",
                  background: "transparent",
                  color: "#8b3a2a",
                  cursor: "pointer",
                  fontSize: 16,
                  fontFamily: "inherit",
                }}
              >
                ✕
              </button>
            </div>
          )}

          <div ref={messagesEndRef} />
        </div>
      </div>

      {/* Input Area */}
      <div style={{ padding: "12px 16px 20px", flexShrink: 0 }}>
        <div
          style={{
            maxWidth: 680,
            margin: "0 auto",
            background: "var(--input-bg)",
            borderRadius: 20,
            border: "1px solid var(--border-color)",
            padding: "12px 16px",
            display: "flex",
            alignItems: "flex-end",
            gap: 8,
            transition: "border-color 0.2s",
          }}
        >
          <textarea
            ref={textareaRef}
            value={input}
            onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => {
              setInput(e.target.value);
              e.target.style.height = "auto";
              e.target.style.height = Math.min(e.target.scrollHeight, 160) + "px";
            }}
            onKeyDown={handleKeyDown}
            placeholder="Send a message..."
            rows={1}
            style={{
              flex: 1,
              border: "none",
              background: "transparent",
              color: "var(--text-primary)",
              fontSize: 15,
              fontFamily: "inherit",
              resize: "none",
              lineHeight: 1.5,
              padding: "2px 0",
              maxHeight: 160,
            }}
          />
          {isStreaming ? (
            <button
              onClick={stop}
              style={{
                width: 32,
                height: 32,
                borderRadius: "50%",
                border: "none",
                background: "var(--accent)",
                cursor: "pointer",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
                transition: "background 0.15s",
              }}
              onMouseEnter={(e) => (e.currentTarget.style.background = "var(--accent-hover)")}
              onMouseLeave={(e) => (e.currentTarget.style.background = "var(--accent)")}
            >
              <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
                <rect x="1" y="1" width="10" height="10" rx="2" fill="white" />
              </svg>
            </button>
          ) : (
            <button
              onClick={handleSend}
              disabled={!input.trim()}
              style={{
                width: 32,
                height: 32,
                borderRadius: "50%",
                border: "none",
                background: input.trim() ? "var(--accent)" : "var(--bg-tertiary)",
                cursor: input.trim() ? "pointer" : "default",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
                transition: "background 0.15s, transform 0.1s",
              }}
              onMouseEnter={(e) => {
                if (input.trim()) e.currentTarget.style.background = "var(--accent-hover)";
              }}
              onMouseLeave={(e) => {
                if (input.trim()) e.currentTarget.style.background = "var(--accent)";
              }}
            >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <path
                  d="M8 12V4M8 4L4 8M8 4l4 4"
                  stroke="white"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
          )}
        </div>
        <div style={{ textAlign: "center", marginTop: 8, fontSize: 11, color: "var(--text-muted)" }}>
          Models can make mistakes. Models may produce inaccurate information.
        </div>
      </div>
    </>
  );
}
