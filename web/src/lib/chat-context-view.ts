import { getLocale, translateIn } from "../i18n/runtime.ts";
import type { Locale } from "../i18n/config.ts";
import type { ChatContextView, ChatControlState } from "./types.ts";
export const chatCompactDefaults = { focus: "", keep_recent: 2 };

// A partial or older context payload must not reach rendering: consumers read
// numeric fields directly (output_reserve.toLocaleString, window comparisons).
export function normalizeChatContextView(view: ChatContextView): ChatContextView {
  return {
    ...view,
    measured_input: view.measured_input ?? 0,
    reserve_estimated: view.reserve_estimated ?? true,
    before_tokens: view.before_tokens ?? 0,
    after_tokens: view.after_tokens ?? 0,
    run_id: view.run_id ?? "",
    history_revision: view.history_revision ?? "0",
    sequence: view.sequence ?? "0",
    profile_id: view.profile_id ?? "",
    model: view.model ?? "",
    compactor: view.compactor ?? "",
    source: view.source ?? "unobserved",
    summary: view.summary ?? "",
    updated_at: view.updated_at ?? "",
    window: view.window ?? 0,
    used: view.used ?? 0,
    output_reserve: view.output_reserve ?? 0,
    headroom: view.headroom ?? 0,
    compression_count: view.compression_count ?? 0,
    estimated: view.estimated ?? true,
    categories: view.categories ?? [],
  };
}

export function canCompactChatContext(state: ChatControlState | null, view: ChatContextView | null) {
  return (
    !!state?.capabilities.compact &&
    !state.active &&
    !state.context_changing &&
    !state.inputs.some((item) => item.status === "unknown") &&
    !!view &&
    view.source !== "unobserved"
  );
}
export function formatChatContext(view: ChatContextView | null, locale: Locale = getLocale()) {
  const t = (key: Parameters<typeof translateIn>[1]) => translateIn(locale, key);
  const number = (n: number) => new Intl.NumberFormat(locale).format(n);
  const unknown = !view || view.source === "unobserved";
  const label = view?.estimated ? t("interface.chatContextEstimated") : t("interface.chatContextMeasured");
  const names: Record<string, Parameters<typeof translateIn>[1]> = {
    system: "interface.chatContextSystem",
    tools: "interface.chatContextTools",
    skills: "interface.chatContextSkills",
    mcp: "interface.chatContextMCP",
    conversation: "interface.chatContextConversation",
    documents: "interface.chatContextDocuments",
    memory: "interface.chatContextMemory",
    other: "interface.chatContextOther",
  };
  return {
    usage: unknown
      ? t("interface.chatContextUnknown")
      : `${number(view.used)} / ${view.window > 0 ? number(view.window) : "?"} · ${label}`,
    headroom: unknown || view.window <= 0 ? "?" : number(view.headroom),
    categories: (view?.categories ?? []).map((category) => ({
      name: t(names[category.name] ?? names.other),
      label: `${number(category.tokens)} · ${t("interface.chatContextEstimated")}`,
    })),
  };
}
