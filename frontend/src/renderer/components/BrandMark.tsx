import { BRAND_NAME } from "../../shared/brand";
import { cn } from "../lib/utils";
import { X00Mark } from "./X00Portrait";

/** The X-ROBOT-AI mark: X-00's head on a void disc. Decorative. */
export function XRobotMark({ className }: { className?: string }) {
	return <X00Mark className={className} />;
}

/** The product name, set in the deck's wordmark face. */
export function XRobotWordmark({ className }: { className?: string }) {
	return <span className={cn("xr-wordmark", className)}>{BRAND_NAME}</span>;
}
