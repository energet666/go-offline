<script lang="ts">
  import ProxyInstructions from "./lib/components/ProxyInstructions.svelte";
  import ModulePrefetch from "./lib/components/ModulePrefetch.svelte";
  import GoModPrefetch from "./lib/components/GoModPrefetch.svelte";
  import ProxyConsole from "./lib/components/ProxyConsole.svelte";
  import CachedModules from "./lib/components/CachedModules.svelte";
  import Toast from "./lib/components/Toast.svelte";

  let proxyUrl = (window as any).__PROXY_URL__ || "http://127.0.0.1:8080";
</script>

<div class="max-w-6xl mx-auto py-8 px-4 text-slate-800">
  <h1 class="text-4xl font-bold mb-2 text-slate-900 tracking-tight">
    go-offline
  </h1>
  <p class="text-slate-600 mb-6">
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
