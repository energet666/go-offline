import { writable, get } from "svelte/store";
import { fetchJSON } from "./utils";

// Toast state and helper
export const toastStore = writable({ show: false, message: "" });
export const isDownloadingStore = writable(false);
let toastTimeout: any;

export function showToastMessage(msg: string) {
	toastStore.set({ show: true, message: msg });
	if (toastTimeout) clearTimeout(toastTimeout);
	toastTimeout = setTimeout(() => {
		toastStore.update(s => ({ ...s, show: false }));
	}, 2500);
}

// Module cache state
export interface CachedModule {
	module: string;
	version: string;
	time?: string;
	pinned?: boolean;
	exported?: boolean;
}

export const modulesStore = writable<CachedModule[]>([]);
export const unexportedCountStore = writable(0);
export const modulesQueryStore = writable("");

export async function loadModules(query?: string) {
	try {
		const q = (query ?? get(modulesQueryStore)).trim();
		const url = q ? `/api/modules?q=${encodeURIComponent(q)}` : "/api/modules";
		const data = await fetchJSON(url);
		if (data && typeof data === "object" && "modules" in data) {
			modulesStore.set(data.modules);
			unexportedCountStore.set(data.unexported_count || 0);
		} else {
			// Fallback for old API if needed (though we just updated it)
			modulesStore.set(data);
		}
	} catch (err) {
		console.error("Failed to load modules", err);
	}
}

// Updates for pinned packages (requires internet access to the upstream proxy)
export interface ModuleUpdate {
	module: string;
	version: string;
	latest?: string;
	published_at?: string;
	next_major_module?: string;
	next_major_version?: string;
	has_update: boolean;
	error?: string;
}

// key: "module@version" of the pinned entry
export const updatesStore = writable<Record<string, ModuleUpdate>>({});
export const updatesCheckedAtStore = writable("");
export const updatesLoadingStore = writable(false);

export async function checkUpdates(force = false) {
	updatesLoadingStore.set(true);
	try {
		const data = await fetchJSON(
			force ? "/api/pinned/updates?force=1" : "/api/pinned/updates"
		);
		const byKey: Record<string, ModuleUpdate> = {};
		for (const u of data.updates || []) {
			byKey[`${u.module}@${u.version}`] = u;
		}
		updatesStore.set(byKey);
		updatesCheckedAtStore.set(data.checked_at || "");
		return data;
	} finally {
		updatesLoadingStore.set(false);
	}
}

// Self-update of the application itself (requires internet access to GitHub)
export interface AppBuild {
	version: string;
	built_at?: string;
}

export interface SelfUpdateStatus {
	current: AppBuild;
	latest?: AppBuild;
	has_update: boolean;
	can_update: boolean;
	reason?: string;
}

export async function pinModule(module: string, version: string) {
	try {
		await fetchJSON("/api/pinned", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ module, version }),
		});
		await loadModules();
	} catch (err) {
		console.error("Failed to pin module", err);
		throw err;
	}
}

export async function unpinModule(module: string, version: string) {
	try {
		await fetchJSON("/api/pinned", {
			method: "DELETE",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ module, version }),
		});
		await loadModules();
	} catch (err) {
		console.error("Failed to unpin module", err);
		throw err;
	}
}
