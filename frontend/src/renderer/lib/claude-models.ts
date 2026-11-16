// The Claude API models offered by Mission control and X-00, newest first.
// Any other model ID can still be typed in. Update when Anthropic releases or
// retires a model; IDs are the Claude API's own.
export type ClaudeModelChoice = { id: string; label: string };

export const CLAUDE_MODELS: readonly ClaudeModelChoice[] = [
	{ id: "claude-fable-5-2", label: "Claude Fable 5.2" },
	{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
	{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
	{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
];

/** The list with `fallback` marked as what an empty setting uses. */
export function claudeModelChoices(fallback: string) {
	return CLAUDE_MODELS.map((model) => ({ ...model, isDefault: model.id === fallback }));
}
