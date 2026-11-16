import type { ChatControlState, ChatSessionRef } from "@/lib/types";

// The control endpoint is authoritative, but a partial or older payload must not
// crash the chat surface: queue consumers read inputs/capabilities synchronously.
// Normalize once at the transport boundary so every consumer sees full arrays.
export function normalizeChatControlState(
  state: ChatControlState | null | undefined,
  fallback: ChatSessionRef,
): ChatControlState {
  return {
    ...state,
    context_changing: state?.context_changing ?? false,
    session: state?.session ?? fallback,
    revision: state?.revision ?? "0",
    history_revision: state?.history_revision ?? "0",
    paused: state?.paused ?? false,
    active: state?.active ?? null,
    inputs: state?.inputs ?? [],
    capabilities: state?.capabilities ?? {},
  };
}
