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

<div
    class="card bg-base-100/80 backdrop-blur-md shadow-2xl border border-base-content/10 mb-8"
>
    <div class="card-body">
        <h3 class="card-title text-xl font-bold">Консоль прокси-запросов</h3>
        <p class="text-sm opacity-70">
            Показывает обращения Go к этому GOPROXY (метод, путь, статус, время,
            user-agent).
        </p>
        <div
            class="flex gap-2 items-center mt-3 border-b border-base-content/10 pb-4"
        >
            <button
                class="btn btn-sm btn-primary shadow-lg shadow-primary/20"
                onclick={loadProxyLogs}>Обновить</button
            >
            <button
                class="btn btn-sm btn-outline opacity-80"
                onclick={() => (proxyLogLines = [])}>Очистить экран</button
            >
            <span
                class="text-sm opacity-60 ml-3 bg-base-200/50 px-2 py-1 rounded-md"
                >{proxyStatus}</span
            >
        </div>
        <div
            class="mt-4 bg-neutral/90 text-neutral-content p-4 rounded-xl h-48 flex flex-col-reverse overflow-y-auto font-mono text-xs whitespace-pre-wrap shadow-inner"
        >
            <div>
                {#if proxyLogLines.length > 0}
                    {proxyLogLines.join("\n")}
                {:else}
                    <span class="opacity-50 italic"
                        >Ожидание запросов к прокси...</span
                    >
                {/if}
            </div>
        </div>
    </div>
</div>
