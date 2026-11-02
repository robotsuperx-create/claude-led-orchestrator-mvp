// The crew from the X-ROBOT deck: sixteen robots, X-01 to X-16, who carry out
// the steps of X-00's (Claude's) plan. A step's crew member is fixed by its
// place in the plan, so the same plan always shows the same crew.

const portraits = import.meta.glob<string>("../assets/x00/crew/x*.webp", { eager: true, import: "default" });

export const CREW_SIZE = 16;

export type CrewMember = { callSign: string; portrait: string | undefined };

/** The crew member for the step at `index` (0-based); the crew repeats after X-16. */
export function crewMember(index: number): CrewMember {
	const number = (((Math.trunc(index) % CREW_SIZE) + CREW_SIZE) % CREW_SIZE) + 1;
	const id = String(number).padStart(2, "0");
	return { callSign: `X-${id}`, portrait: portraits[`../assets/x00/crew/x${id}.webp`] };
}
