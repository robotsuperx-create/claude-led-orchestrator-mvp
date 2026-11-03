import type { X00ChatInput, X00Heard, X00Info, X00ListenInput, X00Reply, X00Result, X00SpeakInput, X00Speech } from "../../shared/x00";

/** The bridge surface the renderer depends on (matches preload's shape). */
export type X00Bridge = {
	info: () => Promise<X00Result<X00Info>>;
	chat: (input: X00ChatInput) => Promise<X00Result<X00Reply>>;
	listen: (input: X00ListenInput) => Promise<X00Result<X00Heard>>;
	speak: (input: X00SpeakInput) => Promise<X00Result<X00Speech>>;
};

/** Used outside Electron when no preview data is wanted (tests, plain web). */
export const unavailableX00Bridge: X00Bridge = {
	info: async () => ({ ok: true, value: { enabled: false, reason: "no_key" } }),
	chat: async () => ({ ok: false, error: "disabled" }),
	listen: async () => ({ ok: false, error: "disabled" }),
	speak: async () => ({ ok: false, error: "disabled" }),
};

/**
 * A scripted X-00 for the browser preview build (dev:web), which has no daemon
 * or Claude key. The packaged app never uses it.
 */
export function createX00Preview(): X00Bridge {
	return {
		// The preview hears (a scripted line) but speaks with the browser's voice.
		info: async () => ({ ok: true, value: { enabled: true, model: "claude-haiku-5-5", listen: "local" } }),
		listen: async (input) => {
			await new Promise((resolve) => setTimeout(resolve, 700));
			return { ok: true, value: { text: input.locale.startsWith("ar") ? "ما حال الأسطول؟" : "How is the fleet?" } };
		},
		speak: async () => ({ ok: false, error: "disabled" }),
		chat: async (input) => {
			await new Promise((resolve) => setTimeout(resolve, 900));
			const last = input.turns[input.turns.length - 1]?.content ?? "";
			const arabic = /[؀-ۿ]/.test(last) || input.locale.startsWith("ar");
			if (/run|شغ|مهم|ابدأ|start/i.test(last)) {
				return {
					ok: true,
					value: {
						say: arabic ? "جهّزت لك مهمة جديدة. راجعها واضغط نفّذ لأطلقها مع خطة تنتظر موافقتك." : "I have a new run ready. Review it and press Run to launch it with a plan for your approval.",
						mood: "neutral",
						actions: [{ type: "start_run", task: arabic ? "أضف نقطة فحص صحة /healthz للخدمة مع اختبار." : "Add a /healthz health check endpoint with a test.", approvePlan: true }],
					},
				};
			}
			return {
				ok: true,
				value: {
					say: arabic
						? "أهلاً أيها القائد. الأسطول هادئ: تشغيل واحد جاهز للدمج، وآخر معلّق للمراجعة، وثالث فشل لنفاد الميزانية."
						: "Welcome, commander. The fleet is calm: one run is ready to merge, one is held for review, and one stopped when its budget ran out.",
					mood: "pleased",
					actions: [{ type: "open", screen: "mission_control" }],
				},
			};
		},
	};
}
