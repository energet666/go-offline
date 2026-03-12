<script lang="ts">
	import { onMount, onDestroy } from "svelte";
	import { fetchJSON } from "../utils";
	import { ChevronRight } from "lucide-svelte";

	let proxyRequests = $state<any[]>([]);
	let proxyStatus = $state("");
	let proxyLogLines = $state<string[]>([]);
	let proxyLogInterval: any;

	const STORAGE_KEY = "go-offline:console-expanded";
	let isExpanded = $state((() => {
		const stored = localStorage.getItem(STORAGE_KEY);
		return stored === null ? true : stored === "true";
	})());

	function toggle() {
		isExpanded = !isExpanded;
		localStorage.setItem(STORAGE_KEY, String(isExpanded));
	}

	async function loadProxyLogs() {
		if (!isExpanded) return;
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
		<button
			class="w-full text-left flex items-center gap-2 group select-none"
			onclick={toggle}
		>
			<span
				class="transition-transform duration-200 opacity-50 group-hover:opacity-80"
				class:rotate-90={isExpanded}
			>
				<ChevronRight size={18} />
			</span>
			<h3 class="card-title text-xl font-bold">Консоль прокси-запросов</h3>
		</button>

		{#if isExpanded}
			<p class="text-sm opacity-70 mt-2">
				Показывает обращения Go к этому GOPROXY (метод, путь, статус, время,
				user-agent).
			</p>
			<div
				class="flex gap-2 items-center mt-3 border-b border-base-content/10 pb-4"
			>
				<span class="text-sm opacity-60 bg-base-200/50 px-2 py-1 rounded-md"
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
						<span class="opacity-50 italic">Ожидание запросов к прокси...</span>
					{/if}
				</div>
			</div>
		{/if}
	</div>
</div>
