import { isDownloadingStore } from "./stores";

function copyWithExecCommand(text: string): boolean {
	const textarea = document.createElement("textarea");
	textarea.value = text;
	textarea.style.position = "fixed";
	textarea.style.opacity = "0";
	document.body.appendChild(textarea);
	textarea.focus();
	textarea.select();
	let ok = false;
	try {
		ok = document.execCommand("copy");
	} catch {
		ok = false;
	}
	document.body.removeChild(textarea);
	return ok;
}

// navigator.clipboard requires a secure context (HTTPS or localhost/127.0.0.1);
// Chrome makes it undefined or throws on any other origin, so we fall back to
// the legacy execCommand approach to keep copy buttons working there too.
export async function copyToClipboard(text: string): Promise<boolean> {
	if (navigator.clipboard?.writeText) {
		try {
			await navigator.clipboard.writeText(text);
			return true;
		} catch (err) {
			console.error("navigator.clipboard.writeText failed, falling back", err);
		}
	}
	return copyWithExecCommand(text);
}

export async function fetchJSON(url: string, options?: RequestInit) {
	const res = await fetch(url, options);
	const data = await res.json();
	if (!res.ok) throw new Error(data.error || "request failed");
	return data;
}

export async function cancelDownload() {
	return fetchJSON("/api/download-cancel", { method: "POST" });
}

export async function watchDownload(
	onStatus: (status: string) => void,
	onLog: (logs: string[]) => void,
	onDone: () => void
) {
	isDownloadingStore.set(true);
	let done = false;
	while (!done) {
		try {
			const state = await fetchJSON("/api/download-status");
			const prefix = `[${state.status}] `;

			let statusText = "";
			if (state.status === "done") {
				statusText = prefix + (state.message || "Готово");
				done = true;
				onDone();
			} else if (state.status === "error") {
				statusText = prefix + (state.error || "Ошибка");
				done = true;
			} else if (state.status === "idle") {
				statusText = "Ожидание...";
				done = true;
			} else {
				statusText = prefix + (state.message || "Выполняется...");
			}

			onStatus(statusText);
			onLog(state.logs || []);
		} catch (e: any) {
			onStatus("Ошибка статуса: " + e.message);
			done = true;
		}
		if (!done) {
			await new Promise((r) => setTimeout(r, 1000));
		}
	}
	isDownloadingStore.set(false);
}
