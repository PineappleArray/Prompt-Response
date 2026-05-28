import { useState, useRef, useEffect } from "react";
import ChatView from "./ChatView";
import MetricsPage from "./MetricsPage";
import { shellStyle } from "./theme";

// ── Types ──────────────────────────────────────────────────────────
type Model = {
  id: string;
  label: string;
  badge: string;
};

type View = "chat" | "usage";

// ── Constants ──────────────────────────────────────────────────────
const MODELS: Model[] = [
  { id: "opus", label: "Claude Opus 4.6", badge: "Most Capable" },
  { id: "sonnet", label: "Claude Sonnet 4.6", badge: "Balanced" },
  { id: "haiku", label: "Claude Haiku 4.5", badge: "Fastest" },
];

// ── Nav tabs ───────────────────────────────────────────────────────
function NavTabs({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  const tabs: { id: View; label: string }[] = [
    { id: "chat", label: "Chat" },
    { id: "usage", label: "Usage" },
  ];
  return (
    <div style={{ display: "flex", gap: 4 }}>
      {tabs.map((tab) => {
        const active = view === tab.id;
        return (
          <button
            key={tab.id}
            onClick={() => onChange(tab.id)}
            style={{
              padding: "6px 14px",
              border: "none",
              background: active ? "var(--bg-tertiary)" : "transparent",
              borderRadius: 10,
              cursor: "pointer",
              color: active ? "var(--text-primary)" : "var(--text-secondary)",
              fontSize: 15,
              fontWeight: 600,
              fontFamily: "inherit",
              letterSpacing: "-0.02em",
              transition: "background 0.15s, color 0.15s",
            }}
            onMouseEnter={(e) => {
              if (!active) (e.currentTarget as HTMLButtonElement).style.background = "var(--bg-hover)";
            }}
            onMouseLeave={(e) => {
              if (!active) (e.currentTarget as HTMLButtonElement).style.background = "transparent";
            }}
          >
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}

// ── Main shell ─────────────────────────────────────────────────────
export default function App() {
  const [view, setView] = useState<View>("chat");
  const [selectedModel, setSelectedModel] = useState<string>("sonnet");
  const [modelDropdownOpen, setModelDropdownOpen] = useState<boolean>(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent): void {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setModelDropdownOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  const currentModel = MODELS.find((m) => m.id === selectedModel);

  return (
    <div style={shellStyle}>
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

      {/* Top Bar: nav tabs (left) + model selector (right, chat only) */}
      <div
        style={{
          height: 52,
          borderBottom: "1px solid var(--border-color)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "0 16px",
          flexShrink: 0,
          background: "var(--bg-primary)",
          position: "relative",
          zIndex: 100,
        }}
      >
        <NavTabs view={view} onChange={setView} />

        {view === "chat" && (
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
                  right: 0,
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
        )}
      </div>

      {/* Body */}
      {view === "chat" ? <ChatView selectedModel={selectedModel} /> : <MetricsPage />}
    </div>
  );
}
