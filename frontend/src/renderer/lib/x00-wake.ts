// X-00's wake word, for hands-free talk: a line is meant for him when it
// starts with his name, however a speech engine wrote it down ("X-00",
// "X zero", "Ex double zero", "إكس زيرو", "اكس صفر صفر").

const LETTER = String.raw`(?:x|ex|eks|إكس|اكس|أكس|ايكس|إيكس)`;
const ZERO = String.raw`(?:0{1,2}|zero|double[\s-]*zero|oh[\s-]*oh|زيرو|صفر(?:[\s-]*صفر)?|٠{1,2})`;
const WAKE = new RegExp(String.raw`^\s*(?:(?:hey|ok|okay|يا|هاي)[\s,،]+)?${LETTER}[\s.\-–]*${ZERO}(?=$|[\s,،.:!?؟-])[\s,،.:!?؟-]*`, "iu");

/**
 * The command after the wake word: "" when the person only called him,
 * null when the line was not for him.
 */
export function wakeCommand(text: string): string | null {
	const match = WAKE.exec(text);
	if (!match) return null;
	return text.slice(match[0].length).trim();
}
