<script lang="ts">
  import { Download, Upload, Loader2 } from "lucide-svelte";
  import ProxyInstructions from "./lib/components/ProxyInstructions.svelte";
  import ModulePrefetch from "./lib/components/ModulePrefetch.svelte";
  import GoModPrefetch from "./lib/components/GoModPrefetch.svelte";
  import ProxyConsole from "./lib/components/ProxyConsole.svelte";
  import CachedModules from "./lib/components/CachedModules.svelte";
  import Toast from "./lib/components/Toast.svelte";
  import { showToastMessage, loadModules } from "./lib/stores";

  let proxyUrl = (window as any).__PROXY_URL__ || "http://127.0.0.1:8080";

  let exportingFull = false;
  let exportingInc = false;
  let importing = false;
  let fileInput: HTMLInputElement;

  async function exportCache(incremental: boolean) {
    if (incremental) {
      exportingInc = true;
    } else {
      exportingFull = true;
    }
    try {
      const qs = incremental ? "?incremental=true" : "";
      const res = await fetch(`/api/export-cache${qs}`);
      if (res.status === 204) {
        showToastMessage("Нет новых пакетов для экспорта");
        return;
      }
      if (!res.ok) {
        const err = await res
          .json()
          .catch(() => ({ error: "Ошибка экспорта" }));
        throw new Error(err.error || "Ошибка экспорта");
      }
      const blob = await res.blob();
      const disposition = res.headers.get("Content-Disposition") || "";
      const match = disposition.match(/filename="?([^"]+)"?/);
      const filename = match ? match[1] : "go-offline-cache.tar.gz";

      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);

      showToastMessage("Архив кэша скачан!");
      await loadModules();
    } catch (err: any) {
      showToastMessage("Ошибка: " + err.message);
    } finally {
      if (incremental) {
        exportingInc = false;
      } else {
        exportingFull = false;
      }
    }
  }

  async function importCache() {
    const file = fileInput?.files?.[0];
    if (!file) return;

    importing = true;
    try {
      const formData = new FormData();
      formData.append("archive", file);

      const res = await fetch("/api/import-cache", {
        method: "POST",
        body: formData,
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Ошибка импорта");
      }

      showToastMessage(data.message || "Импорт завершён!");
      await loadModules();
    } catch (err: any) {
      showToastMessage("Ошибка: " + err.message);
    } finally {
      importing = false;
      // Reset file input so the same file can be re-selected.
      if (fileInput) fileInput.value = "";
    }
  }
</script>

<div class="max-w-6xl mx-auto py-8 px-4 text-base-content">
  <div class="flex items-center justify-between mb-2">
    <h1 class="text-4xl font-extrabold text-primary tracking-tight">
      go-offline
    </h1>
    <div class="flex gap-2">
      <input
        type="file"
        accept=".tar.gz,.tgz,application/gzip"
        class="hidden"
        bind:this={fileInput}
        onchange={importCache}
      />
      <button
        class="btn btn-outline btn-sm gap-2"
        onclick={() => fileInput?.click()}
        disabled={importing}
      >
        {#if importing}
          <Loader2 size={16} class="animate-spin" />
          Импорт…
        {:else}
          <Upload size={16} />
          Импорт кэша
        {/if}
      </button>
      <div class="join">
        <button
          class="btn btn-outline btn-primary btn-sm join-item gap-2"
          onclick={() => exportCache(false)}
          disabled={exportingFull || exportingInc}
          title="Экспортировать весь кэш (создаст новую базовую точку)"
        >
          {#if exportingFull}
            <Loader2 size={16} class="animate-spin" />
            Всё…
          {:else}
            <Download size={16} />
            Всё
          {/if}
        </button>
        <button
          class="btn btn-outline btn-secondary btn-sm join-item gap-2"
          onclick={() => exportCache(true)}
          disabled={exportingFull || exportingInc}
          title="Экспортировать только пакеты, добавленные с прошлого экспорта"
        >
          {#if exportingInc}
            <Loader2 size={16} class="animate-spin" />
            Новые…
          {:else}
            <Download size={16} />
            Новые
          {/if}
        </button>
      </div>
    </div>
  </div>
  <p class="text-base-content/70 mb-6 font-light text-lg">
    Локальный GOPROXY + prefetch зависимостей для офлайн-среды.
  </p>

  <ProxyInstructions {proxyUrl} />

  <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
    <ModulePrefetch />
    <GoModPrefetch />
  </div>

  <ProxyConsole />

  <CachedModules {proxyUrl} />
</div>

<Toast />
