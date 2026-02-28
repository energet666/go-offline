<script lang="ts">
  import { Download, Loader2 } from "lucide-svelte";
  import ProxyInstructions from "./lib/components/ProxyInstructions.svelte";
  import ModulePrefetch from "./lib/components/ModulePrefetch.svelte";
  import GoModPrefetch from "./lib/components/GoModPrefetch.svelte";
  import ProxyConsole from "./lib/components/ProxyConsole.svelte";
  import CachedModules from "./lib/components/CachedModules.svelte";
  import Toast from "./lib/components/Toast.svelte";
  import { showToastMessage } from "./lib/stores";

  let proxyUrl = (window as any).__PROXY_URL__ || "http://127.0.0.1:8080";

  let exporting = false;

  async function exportCache() {
    exporting = true;
    try {
      const res = await fetch("/api/export-cache");
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
    } catch (err: any) {
      showToastMessage("Ошибка: " + err.message);
    } finally {
      exporting = false;
    }
  }
</script>

<div class="max-w-6xl mx-auto py-8 px-4 text-base-content">
  <div class="flex items-center justify-between mb-2">
    <h1 class="text-4xl font-extrabold text-primary tracking-tight">
      go-offline
    </h1>
    <button
      class="btn btn-outline btn-primary btn-sm gap-2"
      onclick={exportCache}
      disabled={exporting}
    >
      {#if exporting}
        <Loader2 size={16} class="animate-spin" />
        Архивирование…
      {:else}
        <Download size={16} />
        Экспорт кэша
      {/if}
    </button>
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
