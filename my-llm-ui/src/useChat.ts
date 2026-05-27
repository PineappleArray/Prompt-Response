import { useReducer, useCallback, useRef } from "react";

// ── Types ──────────────────────────────────────────────────────────
export type Message = {
  role: "user" | "assistant";
  content: string;
};

type StreamChunk = {
  type: "token" | "done" | "error";
  content?: string;
  model?: string;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
  };
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
      msgs[msgs.length - 1] = { ...last, content: last.content + action.payload };
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
export function useChat(apiUrl: string = "/v1/stream") {
  const [state, dispatch] = useReducer(reducer, initialState);
  const abortRef = useRef<AbortController | null>(null);

  const send = useCallback(
    async (model: string) => {
      if (!state.input.trim() || state.isStreaming) return;

      // Build messages array including the new user message
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
          body: JSON.stringify({ model, messages: allMessages }),
          signal: controller.signal,
        });

        if (!res.ok) {
          const text = await res.text();
          dispatch({ type: "STREAM_ERROR", payload: `${res.status}: ${text}` });
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
            const line = part.trim();
            if (!line.startsWith("data: ")) continue;

            const json_str = line.slice(6);
            try {
              const chunk: StreamChunk = JSON.parse(json_str);

              switch (chunk.type) {
                case "token":
                  if (chunk.content) {
                    dispatch({ type: "STREAM_TOKEN", payload: chunk.content });
                  }
                  break;
                case "done":
                  dispatch({ type: "END_STREAM" });
                  break;
                case "error":
                  dispatch({
                    type: "STREAM_ERROR",
                    payload: chunk.content ?? "Unknown stream error",
                  });
                  break;
              }
            } catch {
              // skip malformed chunks
            }
          }
        }

        // if we exited the loop without a "done" chunk
        if (state.isStreaming) {
          dispatch({ type: "END_STREAM" });
        }
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