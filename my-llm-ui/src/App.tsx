import { useState, useRef, useEffect, type CSSProperties } from "react";
import { useChat } from "./useChat";

// ── Types ──────────────────────────────────────────────────────────
type Model = {
  id: string;
  label: string;
  badge: string;
};

interface MessageBubbleProps {
  role: string;
  content: string;
  isStreaming: boolean;
}

// ── Constants ──────────────────────────────────────────────────────
const MODELS: Model[] = [
  { id: "opus", label: "Claude Opus 4.6", badge: "Most Capable" },
  { id: "sonnet", label: "Claude Sonnet 4.6", badge: "Balanced" },
  { id: "haiku", label: "Claude Haiku 4.5", badge: "Fastest" },
];

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
            fontFamily: "'Söhne', 'Helvetica Neue', sans-serif",
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

// ── Main Component ─────────────────────────────────────────────────
export default function ClaudeChatUI() {
  // Point this at your Go backend's stream endpoint.
  const { messages, input, isStreaming, error, send, stop, setInput, clearError } =
    useChat("/v1/stream");

  const [selectedModel, setSelectedModel] = useState<string>("sonnet");
  const [modelDropdownOpen, setModelDropdownOpen] = useState<boolean>(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent): void {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setModelDropdownOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  const handleSend = () => {
    if (!input.trim() || isStreaming) return;
    const model = MODELS.find((m) => m.id === selectedModel);
    // send() dispatches ADD_USER_MESSAGE + START_STREAM internally,
    // then streams tokens via STREAM_TOKEN until END_STREAM.
    send(model?.id ?? "sonnet");
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>): void => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const currentModel = MODELS.find((m) => m.id === selectedModel);

  // Detect if the last message is the assistant streaming.
  const lastMsg = messages[messages.length - 1];
  const isLastAssistantStreaming = isStreaming && lastMsg?.role === "assistant";

  const cssVars: Record<string, string> = {
    "--bg-primary": "#f5f3ef",
    "--bg-secondary": "#eae7e1",
    "--bg-tertiary": "#dedad3",
    "--bg-hover": "#e4e0d9",
    "--border-color": "rgba(0,0,0,0.08)",
    "--text-primary": "#2c2924",
    "--text-secondary": "#6b6560",
    "--text-muted": "#9a948e",
    "--accent": "#c96442",
    "--accent-hover": "#b5573a",
    "--user-bubble": "#e4e0d9",
    "--dropdown-bg": "#eae7e1",
    "--input-bg": "#eae7e1",
  };

  return (
    <div
      style={{
        ...cssVars,
        width: "100%",
        height: "100vh",
        display: "flex",
        flexDirection: "column",
        background: "var(--bg-primary)",
        fontFamily: "'Söhne', 'Helvetica Neue', sans-serif",
        color: "var(--text-primary)",
        overflow: "hidden",
        position: "relative",
      } as CSSProperties}
    >
      <style>{`
        @keyframes cursorBlink {
          0%, 100% { opacity: 1; }
          50% { opacity: 0; }
        }
        @keyframes dotPulse {
          0%, 80%, 100% { opacity: 0.3; transform: scale(0.8); }
          40% { opacity: 1; transform: scale(1); }
        }
        @keyframes fadeSlideUp {
          from { opacity: 0; transform: translateY(8px); }
          to { opacity: 1; transform: translateY(0); }
        }
        @keyframes dropdownIn {
          from { opacity: 0; transform: translateY(-4px) scale(0.97); }
          to { opacity: 1; transform: translateY(0) scale(1); }
        }
        textarea::placeholder { color: var(--text-muted); }
        textarea:focus { outline: none; }
        textarea { scrollbar-width: none; }
        textarea::-webkit-scrollbar { display: none; }
      `}</style>

      {/* Top Bar */}
      <div
        style={{
          height: 52,
          borderBottom: "1px solid var(--border-color)",
          display: "flex",
          alignItems: "center",
          padding: "0 16px",
          flexShrink: 0,
          background: "var(--bg-primary)",
          position: "relative",
          zIndex: 100,
        }}
      >
        <div ref={dropdownRef} style={{ position: "relative" }}>
          <button
            onClick={() => setModelDropdownOpen(!modelDropdownOpen)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 12px",
              border: "none",
              background: modelDropdownOpen ? "var(--bg-tertiary)" : "transparent",
              borderRadius: 10,
              cursor: "pointer",
              color: "var(--text-primary)",
              fontSize: 15,
              fontWeight: 600,
              fontFamily: "inherit",
              letterSpacing: "-0.02em",
              transition: "background 0.15s",
            }}
            onMouseEnter={(e) => {
              if (!modelDropdownOpen) (e.currentTarget as HTMLButtonElement).style.background = "var(--bg-hover)";
            }}
            onMouseLeave={(e) => {
              if (!modelDropdownOpen) (e.currentTarget as HTMLButtonElement).style.background = "transparent";
            }}
          >
            {currentModel?.label ?? "Select Model"}
            <svg
              width="12"
              height="12"
              viewBox="0 0 12 12"
              fill="none"
              style={{
                transform: modelDropdownOpen ? "rotate(180deg)" : "rotate(0deg)",
                transition: "transform 0.2s ease",
              }}
            >
              <path d="M3 4.5L6 7.5L9 4.5" stroke="var(--text-secondary)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>

          {modelDropdownOpen && (
            <div
              style={{
                position: "absolute",
                top: "calc(100% + 6px)",
                left: 0,
                background: "var(--dropdown-bg)",
                borderRadius: 12,
                border: "1px solid var(--border-color)",
                boxShadow: "0 8px 30px rgba(0,0,0,0.35)",
                padding: 6,
                minWidth: 240,
                animation: "dropdownIn 0.15s ease-out",
                zIndex: 200,
              }}
            >
              {MODELS.map((model) => (
                <button
                  key={model.id}
                  onClick={() => {
                    setSelectedModel(model.id);
                    setModelDropdownOpen(false);
                  }}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    width: "100%",
                    padding: "10px 12px",
                    border: "none",
                    background: selectedModel === model.id ? "var(--bg-hover)" : "transparent",
                    borderRadius: 8,
                    cursor: "pointer",
                    color: "var(--text-primary)",
                    fontFamily: "inherit",
                    fontSize: 14,
                    textAlign: "left",
                    transition: "background 0.1s",
                  }}
                  onMouseEnter={(e) => (e.currentTarget.style.background = "var(--bg-hover)")}
                  onMouseLeave={(e) =>
                    (e.currentTarget.style.background = selectedModel === model.id ? "var(--bg-hover)" : "transparent")
                  }
                >
                  <div>
                    <div style={{ fontWeight: 550, letterSpacing: "-0.01em" }}>{model.label}</div>
                    <div style={{ fontSize: 12, color: "var(--text-secondary)", marginTop: 2 }}>{model.badge}</div>
                  </div>
                  {selectedModel === model.id && (
                    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                      <path d="M4 8.5L6.5 11L12 5" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  )}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

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
              <span style={{ fontSize: 16, color: "var(--text-muted)", fontWeight: 500 }}>How can I help you today?</span>
            </div>
          )}

          {/* Render all messages. The last assistant message gets the streaming cursor. */}
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

          {/* Show typing dots when streaming just started (empty assistant message) */}
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
                  <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="white" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
                </svg>
              </div>
              <TypingIndicator />
            </div>
          )}

          {/* Error display */}
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
      <div
        style={{
          padding: "12px 16px 20px",
          flexShrink: 0,
        }}
      >
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
            placeholder="Start a Chat..."
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
              {/* Stop icon (square) */}
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
                <path d="M8 12V4M8 4L4 8M8 4l4 4" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          )}
        </div>
        <div style={{ textAlign: "center", marginTop: 8, fontSize: 11, color: "var(--text-muted)" }}>
          Models can make mistakes. Models may produce inaccurate information.
        </div>
      </div>
    </div>
  );
}