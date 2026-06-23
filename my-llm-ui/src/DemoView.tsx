import { useState, useRef, useEffect } from "react";
import { fontStack } from "./theme";

// ─── Types ────────────────────────────────────────────────────────────────────

type Phase = "idle" | "typing" | "classifying" | "streaming" | "paused";

interface DemoStep {
  prompt: string;
  tier: string;
  tierLabel: string;
  model: string;
  score: number;
  taskType: string;
  signals: Record<string, number>;
  response: string;
  reason: string;
}

interface CompletedExchange {
  prompt: string;
  response: string;
  tier: string;
  tierLabel: string;
  model: string;
}

// ─── Config ───────────────────────────────────────────────────────────────────

const TIER_COLOR: Record<string, string> = {
  small: "#10b981",
  code: "#3b82f6",
  medium: "#f59e0b",
  large: "#8b5cf6",
  reasoning: "#ec4899",
};

const STEPS: DemoStep[] = [
  {
    prompt: "What's 2 + 2?",
    tier: "small",
    tierLabel: "Small",
    model: "Qwen2.5-1.5B",
    score: 0.08,
    taskType: "QA",
    signals: { length: 0.05, code: 0.0, reasoning: 0.10, domain: 0.0, creativity: 0.0, constraint: 0.0 },
    response: "2 + 2 = 4.",
    reason: "Simple QA · score 0.08 → small model sufficient",
  },
  {
    prompt: "Write a Python binary search function.",
    tier: "code",
    tierLabel: "Code",
    model: "Qwen2.5-Coder-7B",
    score: 0.43,
    taskType: "Code Generation",
    signals: { length: 0.28, code: 1.0, reasoning: 0.2, domain: 0.0, creativity: 0.0, constraint: 0.3 },
    response:
      "def binary_search(arr, target):\n    lo, hi = 0, len(arr) - 1\n    while lo <= hi:\n        mid = (lo + hi) // 2\n        if arr[mid] == target:\n            return mid\n        elif arr[mid] < target:\n            lo = mid + 1\n        else:\n            hi = mid - 1\n    return -1",
    reason: "Code Generation · code-specialized model selected",
  },
  {
    prompt:
      "Tradeoffs between microservices and a monolith for a sub-millisecond financial trading platform.",
    tier: "large",
    tierLabel: "Large",
    model: "Qwen2.5-72B",
    score: 0.79,
    taskType: "Open QA",
    signals: { length: 0.58, code: 0.0, reasoning: 0.80, domain: 0.90, creativity: 0.1, constraint: 0.5 },
    response:
      "For sub-millisecond latency, a monolith wins on the critical path — inter-service calls add 0.1–2 ms per hop, exhausting your budget before the first order lands.\n\nMicroservices shine for fault isolation and team autonomy at scale, but those benefits compound at 50+ engineers. Extract only what diverges dramatically in failure modes or scaling curves: market-data fanout and order-matching have orthogonal concurrency profiles. Everything else: keep it in-process.",
    reason: "High reasoning (0.80) + domain knowledge (0.90) → large tier",
  },
  {
    prompt: "Prove that √2 is irrational by contradiction.",
    tier: "reasoning",
    tierLabel: "Reasoning",
    model: "QwQ-32B",
    score: 0.93,
    taskType: "Open QA",
    signals: { length: 0.38, code: 0.0, reasoning: 1.0, domain: 0.5, creativity: 0.0, constraint: 0.7 },
    response:
      "Assume √2 = p/q with gcd(p, q) = 1.\n\np² = 2q²  →  p² is even  →  p is even. Write p = 2k.\n\n(2k)² = 2q²  →  q² = 2k²  →  q is even.\n\nBoth p and q are even, contradicting gcd(p, q) = 1. ∎\n\nThe parity argument cascades: evenness propagates p² → p → q² → q, collapsing the coprimality assumption.",
    reason: "Reasoning (1.00) + score (0.93) → chain-of-thought model",
  },
];

// ─── Component ────────────────────────────────────────────────────────────────

export default function DemoView() {
  const [phase, setPhase] = useState<Phase>("idle");
  const [stepIdx, setStepIdx] = useState(0);
  const [typedPrompt, setTypedPrompt] = useState("");
  const [streamedResponse, setStreamedResponse] = useState("");
  const [classifyProgress, setClassifyProgress] = useState(0);
  const [showRouting, setShowRouting] = useState(false);
  const [completed, setCompleted] = useState<CompletedExchange[]>([]);
  const [allDone, setAllDone] = useState(false);

  const timersRef = useRef<ReturnType<typeof setTimeout>[]>([]);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const clearTimers = () => {
    timersRef.current.forEach(clearTimeout);
    timersRef.current = [];
  };

  const schedule = (fn: () => void, delay: number) => {
    const id = setTimeout(fn, delay);
    timersRef.current.push(id);
  };

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [completed, streamedResponse]);

  useEffect(() => () => clearTimers(), []);

  const start = () => {
    clearTimers();
    setCompleted([]);
    setAllDone(false);
    setTypedPrompt("");
    setStreamedResponse("");
    setClassifyProgress(0);
    setShowRouting(false);
    setStepIdx(0);
    setPhase("idle");

    const runStep = (idx: number) => {
      const s = STEPS[idx];
      setStepIdx(idx);
      setTypedPrompt("");
      setStreamedResponse("");
      setClassifyProgress(0);
      setShowRouting(false);
      setPhase("typing");

      // Phase 1: type the prompt
      let charIdx = 0;
      const typeNext = () => {
        charIdx++;
        setTypedPrompt(s.prompt.slice(0, charIdx));
        if (charIdx < s.prompt.length) {
          schedule(typeNext, 22);
        } else {
          // Phase 2: classify
          setPhase("classifying");
          let prog = 0;
          const tick = () => {
            prog = Math.min(prog + 0.04, 1);
            setClassifyProgress(prog);
            if (prog < 1) {
              schedule(tick, 50);
            } else {
              // Show routing decision
              setShowRouting(true);
              schedule(() => {
                // Phase 3: stream response
                setPhase("streaming");
                let rIdx = 0;
                const streamNext = () => {
                  rIdx++;
                  setStreamedResponse(s.response.slice(0, rIdx));
                  if (rIdx < s.response.length) {
                    schedule(streamNext, 10);
                  } else {
                    // Step complete
                    setCompleted((prev) => [
                      ...prev,
                      { prompt: s.prompt, response: s.response, tier: s.tier, tierLabel: s.tierLabel, model: s.model },
                    ]);
                    setTypedPrompt("");
                    setStreamedResponse("");
                    setPhase("paused");
                    if (idx + 1 < STEPS.length) {
                      schedule(() => runStep(idx + 1), 1100);
                    } else {
                      setAllDone(true);
                    }
                  }
                };
                streamNext();
              }, 500);
            }
          };
          tick();
        }
      };
      typeNext();
    };

    // Small initial delay so the reset state renders first
    schedule(() => runStep(0), 120);
  };

  const step = STEPS[stepIdx];
  const tierColor = TIER_COLOR[step?.tier ?? ""] ?? "#6b7280";
  const showInspector = phase !== "idle";
  const showClassifierData = phase === "classifying" || phase === "streaming" || phase === "paused" || allDone;
  const isActive = phase !== "idle" && !allDone;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", overflow: "hidden" }}>
      {/* Control bar */}
      <div
        style={{
          flexShrink: 0,
          padding: "10px 20px",
          borderBottom: "1px solid var(--border-color)",
          display: "flex",
          alignItems: "center",
          gap: 14,
          background: "var(--bg-primary)",
        }}
      >
        <button
          onClick={start}
          style={{
            padding: "6px 16px",
            borderRadius: 8,
            border: "none",
            background: "var(--accent)",
            color: "white",
            fontSize: 13,
            fontWeight: 600,
            cursor: "pointer",
            fontFamily: fontStack,
            transition: "opacity 0.15s",
          }}
          onMouseEnter={(e) => (e.currentTarget.style.opacity = "0.85")}
          onMouseLeave={(e) => (e.currentTarget.style.opacity = "1")}
        >
          {allDone ? "↺  Replay" : phase === "idle" ? "▶  Play Demo" : "↺  Restart"}
        </button>

        {/* Step progress dots */}
        <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
          {STEPS.map((_, i) => {
            const done = allDone || i < stepIdx || (i === stepIdx && phase === "paused");
            const active = !allDone && i === stepIdx && phase !== "idle" && phase !== "paused";
            return (
              <div
                key={i}
                style={{
                  width: active ? 22 : 8,
                  height: 8,
                  borderRadius: 4,
                  background: done || active ? "var(--accent)" : "var(--bg-tertiary)",
                  opacity: done || active ? 1 : 0.35,
                  transition: "width 0.3s ease, opacity 0.3s ease",
                }}
              />
            );
          })}
        </div>

        {isActive && step && (
          <span style={{ fontSize: 12, color: "var(--text-muted)" }}>
            Step {stepIdx + 1} / {STEPS.length}
            {phase === "typing" && "  ·  typing prompt"}
            {phase === "classifying" && "  ·  classifying"}
            {phase === "streaming" && `  ·  streaming via ${step.tierLabel}`}
          </span>
        )}
        {allDone && (
          <span style={{ fontSize: 12, color: "var(--accent)", fontWeight: 600 }}>
            Demo complete ✓
          </span>
        )}
      </div>

      {/* Split body */}
      <div style={{ flex: 1, display: "flex", overflow: "hidden", minHeight: 0 }}>

        {/* ── Left: chat panel ── */}
        <div
          style={{
            flex: "0 0 58%",
            borderRight: "1px solid var(--border-color)",
            display: "flex",
            flexDirection: "column",
            overflow: "hidden",
          }}
        >
          <div style={{ flex: 1, overflowY: "auto", padding: "24px 28px" }}>
            {completed.length === 0 && phase === "idle" && (
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  height: "100%",
                  opacity: 0.35,
                }}
              >
                <span style={{ fontSize: 14, color: "var(--text-muted)" }}>
                  Press ▶ to watch the router in action
                </span>
              </div>
            )}

            {/* Completed exchanges */}
            {completed.map((c, i) => (
              <div key={i} style={{ marginBottom: 28 }}>
                <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                  <div
                    style={{
                      padding: "10px 16px",
                      borderRadius: 20,
                      background: "var(--user-bubble)",
                      color: "var(--text-primary)",
                      fontSize: 14,
                      maxWidth: "82%",
                      fontFamily: fontStack,
                      lineHeight: 1.5,
                    }}
                  >
                    {c.prompt}
                  </div>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 6 }}>
                  <div
                    style={{
                      width: 6,
                      height: 6,
                      borderRadius: "50%",
                      background: TIER_COLOR[c.tier] ?? "#6b7280",
                      boxShadow: `0 0 5px ${TIER_COLOR[c.tier] ?? "#6b7280"}`,
                    }}
                  />
                  <span style={{ fontSize: 11, color: TIER_COLOR[c.tier] ?? "#6b7280", fontWeight: 600 }}>
                    {c.tierLabel} · {c.model}
                  </span>
                </div>
                <div
                  style={{
                    color: "var(--text-primary)",
                    fontSize: 14,
                    lineHeight: 1.7,
                    fontFamily: fontStack,
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-word",
                  }}
                >
                  {c.response}
                </div>
              </div>
            ))}

            {/* In-progress exchange */}
            {(phase === "typing" || phase === "classifying" || phase === "streaming") && step && (
              <div style={{ marginBottom: 28 }}>
                {/* User bubble */}
                <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                  <div
                    style={{
                      padding: "10px 16px",
                      borderRadius: 20,
                      background: "var(--user-bubble)",
                      color: "var(--text-primary)",
                      fontSize: 14,
                      maxWidth: "82%",
                      fontFamily: fontStack,
                      lineHeight: 1.5,
                    }}
                  >
                    {phase === "typing" ? typedPrompt : step.prompt}
                    {phase === "typing" && (
                      <span
                        style={{
                          display: "inline-block",
                          width: 2,
                          height: 14,
                          background: "var(--text-primary)",
                          marginLeft: 1,
                          verticalAlign: "middle",
                          animation: "cursorBlink 0.8s step-end infinite",
                        }}
                      />
                    )}
                  </div>
                </div>

                {/* Typing indicator while classifying */}
                {phase === "classifying" && (
                  <div style={{ display: "flex", gap: 5, padding: "6px 2px", alignItems: "center" }}>
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
                )}

                {/* Streaming response */}
                {phase === "streaming" && (
                  <>
                    <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 6 }}>
                      <div
                        style={{
                          width: 6,
                          height: 6,
                          borderRadius: "50%",
                          background: tierColor,
                          boxShadow: `0 0 5px ${tierColor}`,
                        }}
                      />
                      <span style={{ fontSize: 11, color: tierColor, fontWeight: 600 }}>
                        {step.tierLabel} · {step.model}
                      </span>
                    </div>
                    <div
                      style={{
                        color: "var(--text-primary)",
                        fontSize: 14,
                        lineHeight: 1.7,
                        fontFamily: fontStack,
                        whiteSpace: "pre-wrap",
                        wordBreak: "break-word",
                      }}
                    >
                      {streamedResponse}
                      <span
                        style={{
                          display: "inline-block",
                          width: 2,
                          height: 14,
                          background: tierColor,
                          marginLeft: 1,
                          verticalAlign: "middle",
                          animation: "cursorBlink 0.8s step-end infinite",
                        }}
                      />
                    </div>
                  </>
                )}
              </div>
            )}

            <div ref={messagesEndRef} />
          </div>
        </div>

        {/* ── Right: routing inspector ── */}
        <div
          style={{
            flex: "0 0 42%",
            overflowY: "auto",
            padding: "20px 22px",
            background: "var(--bg-secondary)",
          }}
        >
          {!showInspector ? (
            <div
              style={{
                height: "100%",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                opacity: 0.3,
              }}
            >
              <span style={{ fontSize: 13, color: "var(--text-muted)" }}>Routing inspector</span>
            </div>
          ) : (
            <>
              <div
                style={{
                  fontSize: 10,
                  fontWeight: 700,
                  letterSpacing: "0.1em",
                  color: "var(--text-muted)",
                  textTransform: "uppercase",
                  marginBottom: 14,
                }}
              >
                Routing Inspector
              </div>

              {/* Waiting state during typing */}
              {phase === "typing" && (
                <div style={{ fontSize: 13, color: "var(--text-muted)", opacity: 0.6 }}>
                  Awaiting classification…
                </div>
              )}

              {/* Classifier output */}
              {showClassifierData && step && (
                <div style={{ animation: "fadeSlideUp 0.25s ease" }}>
                  {/* Task type */}
                  <div style={{ marginBottom: 14 }}>
                    <div style={{ fontSize: 11, color: "var(--text-muted)", marginBottom: 5 }}>
                      Task type
                    </div>
                    <span
                      style={{
                        padding: "3px 10px",
                        borderRadius: 6,
                        background: "var(--bg-tertiary)",
                        fontSize: 12,
                        fontWeight: 600,
                        color: "var(--text-primary)",
                      }}
                    >
                      {step.taskType}
                    </span>
                  </div>

                  {/* Complexity score */}
                  <div style={{ marginBottom: 14 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 5 }}>
                      <span style={{ fontSize: 11, color: "var(--text-muted)" }}>
                        Complexity score
                      </span>
                      <span
                        style={{
                          fontSize: 12,
                          fontWeight: 700,
                          color:
                            step.score > 0.65
                              ? "#ef4444"
                              : step.score > 0.35
                              ? "#f59e0b"
                              : "#10b981",
                        }}
                      >
                        {(classifyProgress * step.score).toFixed(2)}
                      </span>
                    </div>
                    <div
                      style={{
                        height: 6,
                        background: "var(--bg-tertiary)",
                        borderRadius: 3,
                        overflow: "hidden",
                      }}
                    >
                      <div
                        style={{
                          width: `${classifyProgress * step.score * 100}%`,
                          height: "100%",
                          background:
                            step.score > 0.65
                              ? "#ef4444"
                              : step.score > 0.35
                              ? "#f59e0b"
                              : "#10b981",
                          borderRadius: 3,
                          transition: "width 0.05s linear",
                        }}
                      />
                    </div>
                  </div>

                  {/* Signal bars */}
                  <div style={{ marginBottom: 14 }}>
                    <div style={{ fontSize: 11, color: "var(--text-muted)", marginBottom: 8 }}>
                      Signals
                    </div>
                    {Object.entries(step.signals).map(([key, val]) => {
                      const displayVal = val * classifyProgress;
                      return (
                        <div
                          key={key}
                          style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}
                        >
                          <span
                            style={{
                              width: 72,
                              fontSize: 11,
                              color: "var(--text-muted)",
                              textAlign: "right",
                              flexShrink: 0,
                              textTransform: "capitalize",
                            }}
                          >
                            {key}
                          </span>
                          <div
                            style={{
                              flex: 1,
                              height: 4,
                              background: "var(--bg-tertiary)",
                              borderRadius: 2,
                              overflow: "hidden",
                            }}
                          >
                            <div
                              style={{
                                width: `${displayVal * 100}%`,
                                height: "100%",
                                background: tierColor,
                                borderRadius: 2,
                                transition: "width 0.05s linear",
                              }}
                            />
                          </div>
                          <span
                            style={{
                              width: 30,
                              fontSize: 11,
                              color: "var(--text-muted)",
                              textAlign: "right",
                            }}
                          >
                            {displayVal.toFixed(2)}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Routing decision */}
              {showRouting && step && (
                <div style={{ animation: "fadeSlideUp 0.3s ease" }}>
                  <div style={{ fontSize: 11, color: "var(--text-muted)", marginBottom: 6 }}>
                    Routing decision
                  </div>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 10,
                      padding: "10px 14px",
                      background: `${tierColor}18`,
                      border: `1px solid ${tierColor}40`,
                      borderRadius: 10,
                      marginBottom: 8,
                    }}
                  >
                    <div
                      style={{
                        width: 8,
                        height: 8,
                        borderRadius: "50%",
                        background: tierColor,
                        boxShadow: `0 0 6px ${tierColor}`,
                        flexShrink: 0,
                      }}
                    />
                    <div
                      style={{ fontSize: 13, fontWeight: 700, color: tierColor, letterSpacing: "-0.01em" }}
                    >
                      {step.tierLabel} tier → {step.model}
                    </div>
                  </div>
                  <div
                    style={{
                      fontSize: 12,
                      color: "var(--text-muted)",
                      lineHeight: 1.55,
                      marginBottom: 12,
                    }}
                  >
                    {step.reason}
                  </div>
                  <div
                    style={{
                      padding: "8px 12px",
                      background: "var(--bg-primary)",
                      borderRadius: 8,
                      border: "1px solid var(--border-color)",
                    }}
                  >
                    <div
                      style={{
                        fontSize: 10,
                        color: "var(--text-muted)",
                        fontWeight: 700,
                        letterSpacing: "0.06em",
                        textTransform: "uppercase",
                        marginBottom: 5,
                      }}
                    >
                      Selected replica
                    </div>
                    <div
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        alignItems: "center",
                        fontSize: 12,
                        color: "var(--text-primary)",
                      }}
                    >
                      <span>replica-{step.tier}-1</span>
                      <span style={{ color: "#10b981", fontWeight: 600, fontSize: 11 }}>
                        ● healthy
                      </span>
                    </div>
                  </div>
                </div>
              )}

              {/* Session summary on completion */}
              {allDone && (
                <div
                  style={{
                    marginTop: 20,
                    padding: "14px",
                    background: "var(--bg-primary)",
                    borderRadius: 10,
                    border: "1px solid var(--border-color)",
                    animation: "fadeSlideUp 0.4s ease",
                  }}
                >
                  <div
                    style={{
                      fontSize: 10,
                      fontWeight: 700,
                      letterSpacing: "0.08em",
                      color: "var(--text-muted)",
                      textTransform: "uppercase",
                      marginBottom: 12,
                    }}
                  >
                    Session usage
                  </div>
                  {STEPS.map((s) => (
                    <div
                      key={s.tier}
                      style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}
                    >
                      <div
                        style={{
                          width: 8,
                          height: 8,
                          borderRadius: "50%",
                          background: TIER_COLOR[s.tier],
                          flexShrink: 0,
                        }}
                      />
                      <span style={{ fontSize: 12, color: "var(--text-primary)", flex: 1 }}>
                        {s.tierLabel}
                      </span>
                      <span style={{ fontSize: 12, color: "var(--text-muted)" }}>{s.model}</span>
                      <span
                        style={{
                          fontSize: 11,
                          color: "var(--text-muted)",
                          background: "var(--bg-tertiary)",
                          padding: "1px 6px",
                          borderRadius: 4,
                        }}
                      >
                        1 req
                      </span>
                    </div>
                  ))}
                  <div
                    style={{
                      marginTop: 10,
                      paddingTop: 10,
                      borderTop: "1px solid var(--border-color)",
                      display: "flex",
                      justifyContent: "space-between",
                      fontSize: 11,
                      color: "var(--text-muted)",
                    }}
                  >
                    <span>{STEPS.length} requests</span>
                    <span>{STEPS.length} tiers used</span>
                    <span>
                      ~{STEPS.reduce((a, s) => a + s.response.split(/\s+/).length, 0)} tokens
                    </span>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
