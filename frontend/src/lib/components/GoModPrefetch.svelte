<script lang="ts">
    import { fetchJSON, watchJob } from "../utils";
    import { loadModules } from "../stores";

    let gomodInput = "";
    let gomodRecursive = false;
    let gomodStatus = "";
    let gomodLog: string[] = [];

    async function startGomodPrefetch() {
        gomodStatus = "Создание задачи...";
        gomodLog = ["Запуск..."];
        try {
            const data = await fetchJSON("/api/prefetch-gomod", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    gomod: gomodInput,
                    recursive: gomodRecursive,
                }),
            });
            gomodStatus = `[queued] Задача #${data.job_id}`;
            watchJob(
                data.job_id,
                (status) => (gomodStatus = status),
                (logs) => (gomodLog = logs),
                () => loadModules(),
            );
        } catch (e: any) {
            gomodStatus = "Ошибка: " + e.message;
        }
    }
</script>

<div class="card bg-base-100 shadow-xl border border-base-200">
    <div class="card-body">
        <h3 class="card-title text-xl">Prefetch из go.mod</h3>
        <div class="form-control w-full">
            <label class="label" for="gomodtext"
                ><span class="label-text">Вставьте содержимое go.mod</span
                ></label
            >
            <textarea
                id="gomodtext"
                class="textarea textarea-bordered h-28 font-mono"
                placeholder="module your/module&#10;go 1.22&#10;require github.com/pkg/errors v0.9.1"
                bind:value={gomodInput}
            ></textarea>
        </div>
        <div class="form-control">
            <label class="label cursor-pointer justify-start gap-3">
                <input
                    type="checkbox"
                    class="checkbox checkbox-primary"
                    bind:checked={gomodRecursive}
                />
                <span class="label-text text-left"
                    >Рекурсивно обходить зависимости из зависимостей (может быть
                    долго)</span
                >
            </label>
        </div>
        <div class="card-actions justify-start items-center mt-2">
            <button class="btn btn-primary" onclick={startGomodPrefetch}
                >Скачать зависимости</button
            >
            <span class="text-sm text-slate-500">{gomodStatus}</span>
        </div>
        <div
            class="mt-4 bg-neutral text-neutral-content p-3 rounded-lg h-40 overflow-y-auto font-mono text-xs whitespace-pre-wrap"
        >
            {#if gomodLog.length > 0}
                {gomodLog.join("\n")}
            {:else}
                Логи появятся здесь.
            {/if}
        </div>
    </div>
</div>
