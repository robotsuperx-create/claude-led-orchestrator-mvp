import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { MediaCreateInput, MediaErrorCode, MediaInfo, MediaItem, MediaResult, MediaServerStatus, MediaUploadInput } from "../../shared/media";
import { aoBridge } from "../lib/bridge";

export const mediaQueryKeys = {
	info: ["media", "info"] as const,
	status: ["media", "status"] as const,
	items: ["media", "items"] as const,
};

/** A failed bridge call; `code` is the only detail the renderer ever sees. */
export class MediaError extends Error {
	constructor(readonly code: MediaErrorCode) {
		super(code);
		this.name = "MediaError";
	}
}

async function unwrap<T>(call: () => Promise<MediaResult<T>>): Promise<T> {
	const result = await call();
	if (!result.ok) throw new MediaError(result.error);
	return result.value;
}

export function mediaErrorCode(error: unknown): MediaErrorCode | null {
	if (!error) return null;
	return error instanceof MediaError ? error.code : "failed";
}

export function useMediaInfo() {
	return useQuery<MediaInfo>({ queryKey: mediaQueryKeys.info, queryFn: () => unwrap(() => aoBridge.media.info()), refetchInterval: 30_000, retry: false });
}

/** Asks the GPU server how it is, only while the gateway is on. */
export function useMediaServerStatus(enabled: boolean) {
	return useQuery<MediaServerStatus>({ queryKey: mediaQueryKeys.status, queryFn: () => unwrap(() => aoBridge.media.status()), enabled, refetchInterval: 30_000, retry: false });
}

/** The library; polls quickly while anything is being made. */
export function useMediaItems() {
	return useQuery<MediaItem[]>({
		queryKey: mediaQueryKeys.items,
		queryFn: () => unwrap(() => aoBridge.media.list()),
		refetchInterval: (query) => (query.state.data?.some((item) => item.state === "queued" || item.state === "running") ? 3_000 : 30_000),
		retry: false,
	});
}

export function useCreateMedia() {
	const client = useQueryClient();
	return useMutation<MediaItem, MediaError, MediaCreateInput>({
		mutationFn: (input) => unwrap(() => aoBridge.media.create(input)),
		onSuccess: () => void client.invalidateQueries({ queryKey: mediaQueryKeys.items }),
	});
}

export function useUploadMedia() {
	const client = useQueryClient();
	return useMutation<MediaItem, MediaError, MediaUploadInput>({
		mutationFn: (input) => unwrap(() => aoBridge.media.upload(input)),
		onSuccess: () => void client.invalidateQueries({ queryKey: mediaQueryKeys.items }),
	});
}

export function useRemoveMedia() {
	const client = useQueryClient();
	return useMutation<true, MediaError, string>({
		mutationFn: (id) => unwrap(() => aoBridge.media.remove(id)),
		onSuccess: () => void client.invalidateQueries({ queryKey: mediaQueryKeys.items }),
	});
}

/** Where a library file is shown (the xrobot-media: scheme, or the preview's stand-in). */
export function mediaUrl(itemId: string, name: string): string {
	return aoBridge.media.fileUrl(itemId, name);
}
