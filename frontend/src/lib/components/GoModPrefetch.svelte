<script lang="ts">
    import { fetchJSON, watchJob } from "../utils";
    import { loadModules } from "../stores";

    let gomodInput = "";
    let gomodRecursive = true;
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

<div
    class="card bg-base-100/80 backdrop-blur-md shadow-2xl border border-base-content/10"
>
    <div class="card-body">
        <h3 class="card-title text-xl font-bold">Prefetch из go.mod</h3>
        <div class="form-control w-full">
            <label class="label" for="gomodtext"
                ><span class="label-text opacity-70"
                    >Вставьте содержимое go.mod</span
                ></label
            >
            <textarea
                id="gomodtext"
                class="textarea textarea-bordered h-28 font-mono bg-base-200/50"
                placeholder="module your/module&#10;go 1.22&#10;require github.com/pkg/errors v0.9.1"
                bind:value={gomodInput}
            ></textarea>
        </div>
        <div class="form-control">
            <label class="label cursor-pointer justify-start gap-3 mt-2">
                <input
                    type="checkbox"
                    class="checkbox checkbox-primary checkbox-sm"
                    bind:checked={gomodRecursive}
                />
                <span class="label-text text-left opacity-80"
                    >Рекурсивно обходить зависимости из зависимостей (может быть
                    долго)</span
                >
            </label>
        </div>
        <div
            class="card-actions justify-start items-center mt-4 border-t border-base-content/10 pt-4"
        >
            <button
                class="btn btn-primary shadow-lg shadow-primary/20"
                onclick={startGomodPrefetch}>Скачать зависимости</button
            >
            <span class="text-sm opacity-60 ml-2">{gomodStatus}</span>
        </div>
        <div
            class="mt-4 bg-neutral/90 text-neutral-content p-4 rounded-xl h-40 overflow-y-auto font-mono text-xs whitespace-pre-wrap shadow-inner"
        >
            {#if gomodLog.length > 0}
                {gomodLog.join("\n")}
            {:else}
                <span class="opacity-50 italic">Логи появятся здесь.</span>
            {/if}
        </div>
    </div>
</div>
