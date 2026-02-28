<script lang="ts">
    import { Copy, Check } from "lucide-svelte";
    import { showToastMessage } from "../stores";
    export let proxyUrl: string;

    let copiedConnect = false;
    let copiedDisconnect = false;

    async function copyCommand(text: string, type: "connect" | "disconnect") {
        try {
            await navigator.clipboard.writeText(text);
            if (type === "connect") copiedConnect = true;
            else copiedDisconnect = true;

            showToastMessage("Скопировано: " + text);

            setTimeout(() => {
                if (type === "connect") copiedConnect = false;
                else copiedDisconnect = false;
            }, 1000);
        } catch (err) {
            console.error("Copy failed", err);
        }
    }
</script>

<div
    class="bg-base-200/50 backdrop-blur-md border border-base-content/10 shadow-sm rounded-2xl p-5 mb-8"
>
    <h3 class="font-bold text-xl mb-3 text-primary flex items-center gap-2">
        Как подключить Go к этому прокси
    </h3>
    <p class="text-sm mb-2 opacity-80">Установить переменные:</p>
    <div class="relative mb-4 group">
        <pre
            class="bg-neutral text-success p-3 pl-4 pr-12 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -w GOPROXY={proxyUrl} GOSUMDB=off</pre>
        <button
            class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-ghost btn-sm opacity-0 group-hover:opacity-100 transition-opacity tooltip"
            data-tip="Скопировать команду"
            onclick={() =>
                copyCommand(
                    `go env -w GOPROXY=${proxyUrl} GOSUMDB=off`,
                    "connect",
                )}
        >
            {#if copiedConnect}
                <Check size={16} class="text-success" />
            {:else}
                <Copy size={16} />
            {/if}
        </button>
    </div>
    <p class="text-sm mb-2 opacity-80">
        Вернуть по умолчанию (отключить прокси):
    </p>
    <div class="relative group">
        <pre
            class="bg-neutral text-success p-3 pl-4 pr-12 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -u GOPROXY GOSUMDB</pre>
        <button
            class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-ghost btn-sm opacity-0 group-hover:opacity-100 transition-opacity tooltip"
            data-tip="Скопировать команду"
            onclick={() =>
                copyCommand("go env -u GOPROXY GOSUMDB", "disconnect")}
        >
            {#if copiedDisconnect}
                <Check size={16} class="text-success" />
            {:else}
                <Copy size={16} />
            {/if}
        </button>
    </div>
</div>
