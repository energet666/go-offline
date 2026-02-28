import { writable, get } from "svelte/store";
import { fetchJSON } from "./utils";

// Toast state and helper
export const toastStore = writable({ show: false, message: "" });
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
export const modulesQueryStore = writable("");

export async function loadModules(query?: string) {
    try {
        const q = (query ?? get(modulesQueryStore)).trim();
        const url = q ? `/api/modules?q=${encodeURIComponent(q)}` : "/api/modules";
        const data = await fetchJSON(url);
        modulesStore.set(data);
    } catch (err) {
        console.error("Failed to load modules", err);
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
