import { isDownloadingStore } from "./stores";

export async function copyText(text: string, event: Event) {
	try {
		await navigator.clipboard.writeText(text);
		const btn = event.target as HTMLButtonElement;
		if (btn) {
			const originalText = btn.innerText;
			btn.innerText = "Скопировано!";
			btn.classList.remove("btn-neutral");
			btn.classList.add("btn-success");
			setTimeout(() => {
				btn.innerText = originalText;
				btn.classList.remove("btn-success");
				btn.classList.add("btn-neutral");
			}, 2000);
		}
		return true;
	} catch (err) {
		console.error("Copy failed", err);
		return false;
	}
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
