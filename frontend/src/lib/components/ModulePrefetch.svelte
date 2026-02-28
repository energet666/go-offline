<script lang="ts">
    import { fetchJSON, watchJob } from "../utils";
    import { loadModules } from "../stores";

    let moduleInput = "";
    let versionInput = "";
    let recursivePrefetch = true;
    let prefetchStatus = "";
    let prefetchLog: string[] = [];

    async function startPrefetch() {
        prefetchStatus = "Создание задачи...";
        prefetchLog = ["Запуск..."];
        try {
            const data = await fetchJSON("/api/prefetch", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    module: moduleInput.trim(),
                    version: versionInput.trim(),
                    recursive: recursivePrefetch,
                }),
            });
            prefetchStatus = `[queued] Задача #${data.job_id}`;
            watchJob(
                data.job_id,
                (status) => (prefetchStatus = status),
                (logs) => (prefetchLog = logs),
                () => loadModules(),
            );
        } catch (e: any) {
            prefetchStatus = "Ошибка: " + e.message;
        }
    }
</script>

<div class="card bg-base-100 shadow-xl border border-base-200">
    <div class="card-body">
        <h3 class="card-title text-xl">Prefetch module@version</h3>
        <div class="form-control w-full">
            <label class="label" for="modpath"
                ><span class="label-text">Module path</span></label
            >
            <input
                id="modpath"
                type="text"
                placeholder="github.com/pkg/errors"
                class="input input-bordered w-full"
                bind:value={moduleInput}
            />
        </div>
        <div class="form-control w-full">
            <label class="label" for="modvers"
                ><span class="label-text">Version (optional)</span></label
            >
            <input
                id="modvers"
                type="text"
                placeholder="v0.9.1 или пусто (= latest)"
                class="input input-bordered w-full"
                bind:value={versionInput}
            />
        </div>
        <div class="form-control">
            <label class="label cursor-pointer justify-start gap-3">
                <input
                    type="checkbox"
                    class="checkbox checkbox-primary"
                    bind:checked={recursivePrefetch}
                />
                <span class="label-text"
                    >Скачать рекурсивно зависимости из go.mod</span
                >
            </label>
        </div>
        <div class="card-actions justify-start items-center mt-2">
            <button class="btn btn-primary" onclick={startPrefetch}
                >Скачать</button
            >
            <span class="text-sm text-slate-500">{prefetchStatus}</span>
        </div>
        <div
            class="mt-4 bg-neutral text-neutral-content p-3 rounded-lg h-40 overflow-y-auto font-mono text-xs whitespace-pre-wrap"
        >
            {#if prefetchLog.length > 0}
                {prefetchLog.join("\n")}
            {:else}
                Логи появятся здесь.
            {/if}
        </div>
    </div>
</div>
