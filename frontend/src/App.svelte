<script lang="ts">
  import { onMount } from "svelte";
  import { CircleCheck } from "lucide-svelte";

  let proxyUrl = (window as any).__PROXY_URL__ || "http://127.0.0.1:8080";

  let moduleInput = "";
  let versionInput = "";
  let recursivePrefetch = true;
  let prefetchStatus = "";
  let prefetchLog: string[] = [];

  let gomodInput = "";
  let gomodRecursive = false;
  let gomodStatus = "";
  let gomodLog: string[] = [];

  let proxyRequests: any[] = [];
  let proxyStatus = "";
  let proxyLogLines: string[] = [];

  let modulesQuery = "";
  let cachedModules: any[] = [];

  let toastMessage = "";
  let showToast = false;
  let toastTimeout: any;

  function showToastMessage(msg: string) {
    toastMessage = msg;
    showToast = true;
    if (toastTimeout) clearTimeout(toastTimeout);
    toastTimeout = setTimeout(() => {
      showToast = false;
    }, 2500);
  }

  async function copyText(text: string, event: Event) {
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
    } catch (err) {
      console.error("Copy failed", err);
    }
  }

  let copiedRows: Record<string, boolean> = {};

  async function copyGoGetCommand(module: string, version: string) {
    try {
      await navigator.clipboard.writeText(`go get ${module}@${version}`);
      const key = `${module}@${version}`;
      copiedRows[key] = true;
      copiedRows = { ...copiedRows };
      showToastMessage(`Скопировано: go get ${module}@${version}`);
      setTimeout(() => {
        copiedRows[key] = false;
        copiedRows = { ...copiedRows };
      }, 1000);
    } catch (err) {
      console.error("Copy failed", err);
    }
  }

  async function fetchJSON(url: string, options?: RequestInit) {
    const res = await fetch(url, options);
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "request failed");
    return data;
  }

  async function loadModules() {
    try {
      const q = modulesQuery.trim();
      const url = q
        ? `/api/modules?q=${encodeURIComponent(q)}`
        : "/api/modules";
      cachedModules = await fetchJSON(url);
    } catch (err) {
      console.error("Failed to load modules", err);
    }
  }

  async function loadProxyLogs() {
    try {
      const data = await fetchJSON("/api/proxy-requests?limit=250");
      proxyLogLines = data.lines || [];
      proxyStatus = `Записей: ${data.count || 0}`;
    } catch (e: any) {
      proxyStatus = "Ошибка консоли: " + e.message;
    }
  }

  async function watchJob(jobId: string, type: "prefetch" | "gomod") {
    let done = false;
    while (!done) {
      try {
        const job = await fetchJSON("/api/jobs/" + encodeURIComponent(jobId));
        const prefix = `[${job.state}] `;

        let statusText = "";
        if (job.state === "done") {
          statusText = prefix + (job.message || "Готово");
          done = true;
          loadModules();
        } else if (job.state === "error") {
          statusText = prefix + (job.error || "Ошибка");
          done = true;
        } else {
          statusText = prefix + (job.message || "Выполняется...");
        }

        if (type === "prefetch") {
          prefetchStatus = statusText;
          prefetchLog = job.logs || [];
        } else {
          gomodStatus = statusText;
          gomodLog = job.logs || [];
        }
      } catch (e: any) {
        if (type === "prefetch")
          prefetchStatus = "Ошибка статуса: " + e.message;
        else gomodStatus = "Ошибка статуса: " + e.message;
        done = true;
      }
      if (!done) {
        await new Promise((r) => setTimeout(r, 1000));
      }
    }
  }

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
      watchJob(data.job_id, "prefetch");
    } catch (e: any) {
      prefetchStatus = "Ошибка: " + e.message;
    }
  }

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
      watchJob(data.job_id, "gomod");
    } catch (e: any) {
      gomodStatus = "Ошибка: " + e.message;
    }
  }

  let proxyLogInterval: any;

  onMount(() => {
    loadModules();
    loadProxyLogs();
    proxyLogInterval = setInterval(loadProxyLogs, 1000);
    return () => clearInterval(proxyLogInterval);
  });
</script>

<div class="max-w-6xl mx-auto py-8 px-4 text-slate-800">
  <h1 class="text-4xl font-bold mb-2 text-slate-900 tracking-tight">
    go-offline
  </h1>
  <p class="text-slate-600 mb-6">
    Локальный GOPROXY + prefetch зависимостей для офлайн-среды.
  </p>

  <div class="bg-primary/10 border border-primary/20 rounded-xl p-4 mb-6">
    <h3 class="font-semibold text-lg mb-2 text-primary">
      Как подключить Go к этому прокси
    </h3>
    <p class="text-sm mb-2">Установить переменные:</p>
    <div class="relative mb-3">
      <pre
        class="bg-slate-900 text-green-300 p-3 rounded-lg text-sm overflow-x-auto">go env -w GOPROXY={proxyUrl} GOSUMDB=off</pre>
      <button
        class="absolute top-2 right-2 btn btn-xs btn-neutral w-[100px]"
        onclick={(e) =>
          copyText(`go env -w GOPROXY=${proxyUrl} GOSUMDB=off`, e)}
        >Копировать</button
      >
    </div>
    <p class="text-sm mb-2">Вернуть по умолчанию (отключить прокси):</p>
    <div class="relative">
      <pre
        class="bg-slate-900 text-green-300 p-3 rounded-lg text-sm overflow-x-auto">go env -u GOPROXY GOSUMDB</pre>
      <button
        class="absolute top-2 right-2 btn btn-xs btn-neutral w-[100px]"
        onclick={(e) => copyText("go env -u GOPROXY GOSUMDB", e)}
        >Копировать</button
      >
    </div>
  </div>

  <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
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

    <div class="card bg-base-100 shadow-xl border border-base-200">
      <div class="card-body">
        <h3 class="card-title text-xl">Prefetch из go.mod</h3>
        <div class="form-control w-full">
          <label class="label" for="gomodtext"
            ><span class="label-text">Вставьте содержимое go.mod</span></label
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
  </div>

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

  <div class="card bg-base-100 shadow-xl border border-base-200">
    <div class="card-body">
      <h3 class="card-title text-xl">Кэшированные модули</h3>
      <div class="my-2">
        <div class="badge badge-success gap-2">GOPROXY={proxyUrl}</div>
      </div>
      <div class="flex gap-2 items-center mb-4">
        <input
          type="text"
          placeholder="Поиск по module/version"
          class="input input-bordered input-sm w-full max-w-xs"
          bind:value={modulesQuery}
          oninput={loadModules}
        />
        <button
          class="btn btn-sm btn-outline"
          onclick={() => {
            modulesQuery = "";
            loadModules();
          }}>Очистить</button
        >
      </div>
      <div class="overflow-x-auto">
        <table class="table table-sm w-full">
          <thead>
            <tr>
              <th>Module</th>
              <th>Version</th>
              <th>Time</th>
            </tr>
          </thead>
          <tbody>
            {#each cachedModules as row}
              <tr
                class="cursor-pointer transition-colors hover:bg-base-200 {copiedRows[
                  `${row.module}@${row.version}`
                ]
                  ? 'bg-success/20!'
                  : ''}"
                title="Нажмите, чтобы скопировать команду go get"
                onclick={() => copyGoGetCommand(row.module, row.version)}
              >
                <td class="break-all">{row.module}</td>
                <td
                  ><div class="badge badge-ghost badge-sm">
                    {row.version}
                  </div></td
                >
                <td class="text-xs text-slate-500">{row.time || ""}</td>
              </tr>
            {/each}
            {#if cachedModules.length === 0}
              <tr>
                <td colspan="3" class="text-center py-4 text-slate-500"
                  >Ничего не найдено</td
                >
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    </div>
  </div>
</div>

{#if showToast}
  <div class="toast toast-top toast-center z-100">
    <div
      class="alert alert-success shadow-lg text-success-content font-medium rounded-xl"
    >
      <CircleCheck class="shrink-0 h-6 w-6" />
      <span>{toastMessage}</span>
    </div>
  </div>
{/if}
