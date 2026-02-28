<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { fetchJSON } from "../utils";

    let proxyRequests: any[] = [];
    let proxyStatus = "";
    let proxyLogLines: string[] = [];
    let proxyLogInterval: any;

    async function loadProxyLogs() {
        try {
            const data = await fetchJSON("/api/proxy-requests?limit=250");
            proxyLogLines = data.lines || [];
            proxyStatus = `Записей: ${data.count || 0}`;
        } catch (e: any) {
            proxyStatus = "Ошибка консоли: " + e.message;
        }
    }

    onMount(() => {
        loadProxyLogs();
        proxyLogInterval = setInterval(loadProxyLogs, 1000);
    });

    onDestroy(() => {
        if (proxyLogInterval) clearInterval(proxyLogInterval);
    });
</script>

<div class="card bg-base-100 shadow-xl border border-base-200 mb-6">
    <div class="card-body">
        <h3 class="card-title text-xl">Консоль прокси-запросов</h3>
        <p class="text-sm text-slate-500">
            Показывает обращения Go к этому GOPROXY (метод, путь, статус, время,
            user-agent).
        </p>
        <div class="flex gap-2 items-center mt-2">
            <button class="btn btn-sm" onclick={loadProxyLogs}>Обновить</button>
            <button
                class="btn btn-sm btn-outline"
                onclick={() => (proxyLogLines = [])}>Очистить экран</button
            >
            <span class="text-sm text-slate-500 ml-2">{proxyStatus}</span>
        </div>
        <div
            class="mt-4 bg-neutral text-neutral-content p-3 rounded-lg h-48 flex flex-col-reverse overflow-y-auto font-mono text-xs whitespace-pre-wrap"
        >
            <div>
                {#if proxyLogLines.length > 0}
                    {proxyLogLines.join("\n")}
                {:else}
                    Ожидание запросов к прокси...
                {/if}
            </div>
        </div>
    </div>
</div>
