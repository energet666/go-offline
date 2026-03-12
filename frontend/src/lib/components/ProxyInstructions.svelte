<script lang="ts">
	import { Copy, Check, ChevronRight } from "lucide-svelte";
	import { showToastMessage } from "../stores";
	interface Props {
		proxyUrl: string;
	}

	let { proxyUrl }: Props = $props();

	let copiedConnect = $state(false);
	let copiedDisconnect = $state(false);

	const STORAGE_KEY = "go-offline:instructions-expanded";
	let isExpanded = $state((() => {
		const stored = localStorage.getItem(STORAGE_KEY);
		return stored === null ? true : stored === "true";
	})());

	function toggle() {
		isExpanded = !isExpanded;
		localStorage.setItem(STORAGE_KEY, String(isExpanded));
	}

	async function copyCommand(text: string, type: "connect" | "disconnect") {
		try {
			await navigator.clipboard.writeText(text);
			if (type === "connect") copiedConnect = true;
			else copiedDisconnect = true;

			showToastMessage("Скопировано: " + text);

			setTimeout(() => {
				if (type === "connect") copiedConnect = false;
				else copiedDisconnect = false;
			}, 1000);
		} catch (err) {
			console.error("Copy failed", err);
		}
	}
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
			<h3 class="card-title text-xl font-bold">
				Как подключить Go к этому прокси
			</h3>
		</button>

		{#if isExpanded}
			<div class="mt-4">
				<p class="text-sm mb-2 opacity-80">Установить переменные:</p>
				<div class="relative mb-4 group">
					<pre
						class="bg-neutral text-success p-3 pl-4 pr-12 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -w GOPROXY={proxyUrl} GOSUMDB=off</pre>
					<button
						class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-ghost btn-sm opacity-0 group-hover:opacity-100 transition-opacity tooltip"
						data-tip="Скопировать команду"
						onclick={(e) => {
							e.stopPropagation();
							copyCommand(`go env -w GOPROXY=${proxyUrl} GOSUMDB=off`, "connect");
						}}
					>
						{#if copiedConnect}
							<Check size={16} class="text-success" />
						{:else}
							<Copy size={16} />
						{/if}
					</button>
				</div>
				<p class="text-sm mb-2 opacity-80">
					Вернуть по умолчанию (отключить прокси):
				</p>
				<div class="relative group">
					<pre
						class="bg-neutral text-success p-3 pl-4 pr-12 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -u GOPROXY GOSUMDB</pre>
					<button
						class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-ghost btn-sm opacity-0 group-hover:opacity-100 transition-opacity tooltip"
						data-tip="Скопировать команду"
						onclick={(e) => {
							e.stopPropagation();
							copyCommand("go env -u GOPROXY GOSUMDB", "disconnect");
						}}
					>
						{#if copiedDisconnect}
							<Check size={16} class="text-success" />
						{:else}
							<Copy size={16} />
						{/if}
					</button>
				</div>
			</div>
		{/if}
	</div>
</div>
