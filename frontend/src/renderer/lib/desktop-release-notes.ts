import { PRODUCT_REPO_SLUG } from "../../shared/brand";

// Release notes come from this product's own repository.
const REPO_PATTERN = PRODUCT_REPO_SLUG.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&");
const RELEASE_ATTRIBUTION_PATTERN = new RegExp(
	`\\s+by\\s+@[A-Za-z0-9-]+(?:\\[bot\\])?\\s+in\\s+(\\[#\\d+\\]\\(https:\\/\\/github\\.com\\/${REPO_PATTERN}\\/pull\\/\\d+\\))\\s*$`,
);
const FULL_CHANGELOG_PATTERN = new RegExp(
	`^\\*\\*Full Changelog\\*\\*:\\s+https:\\/\\/github\\.com\\/${REPO_PATTERN}\\/(?:compare|commits)\\/\\S+\\s*$`,
	"i",
);

export function prepareDesktopReleaseNotes(notes: string): string {
	const lines = notes.split(/\r?\n/).flatMap((line) => {
		if (FULL_CHANGELOG_PATTERN.test(line.trim())) {
			return [];
		}
		return line.replace(RELEASE_ATTRIBUTION_PATTERN, " $1").trimEnd();
	});

	return lines.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}
