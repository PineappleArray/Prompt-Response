import { useReducer, useCallback, useRef } from "react";

// ── Types ──────────────────────────────────────────────────────────
export type Message = {
  role: "user" | "assistant";
  content: string;
};

// OpenAI-compatible SSE chunk structure (what your Go backend proxies through)
type OpenAIChunk = {
  id?: string;
  choices?: Array<{
    delta?: {
      content?: string;
      role?: string;
    };
    finish_reason?: string | null;
  }>;
};

type State = {
  messages: Message[];
  input: string;
  isStreaming: boolean;
  error: string | null;
};

type Action =
  | { type: "SET_INPUT"; payload: string }
  | { type: "ADD_USER_MESSAGE" }
  | { type: "START_STREAM" }
  | { type: "STREAM_TOKEN"; payload: string }
  | { type: "END_STREAM" }
  | { type: "STREAM_ERROR"; payload: string }
  | { type: "CLEAR_ERROR" };

const initialState: State = {
  messages: [],
  input: "",
  isStreaming: false,
  error: null,
};

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "SET_INPUT":
      return { ...state, input: action.payload };

    case "ADD_USER_MESSAGE":
      return {
        ...state,
        input: "",
        messages: [...state.messages, { role: "user", content: state.input }],
      };

    case "START_STREAM":
      return {
        ...state,
        isStreaming: true,
        error: null,
        messages: [...state.messages, { role: "assistant", content: "" }],
      };

    case "STREAM_TOKEN": {
      const msgs = [...state.messages];
      const last = msgs[msgs.length - 1];
      // Trim leading whitespace only from the first chunk of a new message.
      const incoming = last.content === "" ? action.payload.trimStart() : action.payload;
      msgs[msgs.length - 1] = { ...last, content: last.content + incoming };
      return { ...state, messages: msgs };
    }

    case "END_STREAM":
      return { ...state, isStreaming: false };

    case "STREAM_ERROR":
      return { ...state, isStreaming: false, error: action.payload };

    case "CLEAR_ERROR":
      return { ...state, error: null };
  }
}

// ── Hook ───────────────────────────────────────────────────────────
// Posts to /v1/chat/completions with stream: true.
// Your Go backend's ServeHTTP handles this path, proxies to a replica,
// and streams back raw OpenAI-compatible SSE through streamInterceptor.
export function useChat(apiUrl: string = "/v1/chat/completions") {
  const [state, dispatch] = useReducer(reducer, initialState);
  const abortRef = useRef<AbortController | null>(null);

  const send = useCallback(
    async (model: string) => {
      if (!state.input.trim() || state.isStreaming) return;

      const userMessage: Message = { role: "user", content: state.input };
      const allMessages = [...state.messages, userMessage];

      dispatch({ type: "ADD_USER_MESSAGE" });
      dispatch({ type: "START_STREAM" });

      const controller = new AbortController();
      abortRef.current = controller;

      try {
        const res = await fetch(apiUrl, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            model,
            messages: allMessages,
            stream: true,
          }),
          signal: controller.signal,
        });

        if (!res.ok) {
          const text = await res.text();
          let msg = text.trim();
          try {
            const parsed = JSON.parse(text);
            if (parsed?.error?.message) msg = parsed.error.message;
          } catch { /* keep raw */ }
          dispatch({ type: "STREAM_ERROR", payload: `${res.status}: ${msg}` });
          return;
        }

        const reader = res.body?.getReader();
        if (!reader) {
          dispatch({ type: "STREAM_ERROR", payload: "No response stream" });
          return;
        }

        const decoder = new TextDecoder();
        let buffer = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });

          // SSE frames are separated by double newlines
          const parts = buffer.split("\n\n");
          buffer = parts.pop() ?? "";

          for (const part of parts) {
            for (const line of part.split("\n")) {
              const trimmed = line.trim();
              if (!trimmed.startsWith("data: ")) continue;

              const data = trimmed.slice(6);

              // [DONE] sentinel — stream is complete
              if (data === "[DONE]") {
                dispatch({ type: "END_STREAM" });
                continue;
              }

              try {
                const chunk: OpenAIChunk = JSON.parse(data);
                const content = chunk.choices?.[0]?.delta?.content;

                if (content) {
                  dispatch({ type: "STREAM_TOKEN", payload: content });
                }

                // Some providers send finish_reason instead of [DONE]
                const finishReason = chunk.choices?.[0]?.finish_reason;
                if (finishReason === "stop") {
                  dispatch({ type: "END_STREAM" });
                }
              } catch {
                // skip malformed chunks
              }
            }
          }
        }

        // Safety net: if stream ended without [DONE] or finish_reason
        dispatch({ type: "END_STREAM" });
      } catch (err: unknown) {
        if (err instanceof DOMException && err.name === "AbortError") {
          dispatch({ type: "END_STREAM" });
        } else {
          dispatch({
            type: "STREAM_ERROR",
            payload: err instanceof Error ? err.message : "Unknown error",
          });
        }
      } finally {
        abortRef.current = null;
      }
    },
    [state.input, state.messages, state.isStreaming, apiUrl]
  );

  const stop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const setInput = useCallback((value: string) => {
    dispatch({ type: "SET_INPUT", payload: value });
  }, []);

  const clearError = useCallback(() => {
    dispatch({ type: "CLEAR_ERROR" });
  }, []);

  return {
    messages: state.messages,
    input: state.input,
    isStreaming: state.isStreaming,
    error: state.error,
    send,
    stop,
    setInput,
    clearError,
  };
}