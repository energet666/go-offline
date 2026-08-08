<script lang="ts">
	import { cancelDownload, fetchJSON, watchDownload } from "../utils";
	import { loadModules, isDownloadingStore } from "../stores";

	let moduleInput = $state("");
	let versionInput = $state("");
	let prefetchStatus = $state("");
	let prefetchLog = $state<string[]>([]);
	let isRunning = $derived($isDownloadingStore || prefetchStatus.includes("[running]"));

	// A path pasted as "module@version" (how go get spells it) moves its version
	// into the version field instead of being sent as part of the path.
	function splitPastedVersion() {
		const at = moduleInput.indexOf("@");
		if (at < 0) return;
		const version = moduleInput.slice(at + 1).trim();
		const path = moduleInput.slice(0, at).trim();
		if (!path || !version) return;
		if (versionInput.trim() && versionInput.trim() !== version) return;
		moduleInput = path;
		versionInput = version;
	}

	async function startPrefetch() {
		if ($isDownloadingStore) return;
		splitPastedVersion();
		prefetchStatus = "Запуск загрузки...";
		prefetchLog = ["Запуск..."];
		try {
			await fetchJSON("/api/prefetch", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					module: moduleInput.trim(),
					version: versionInput.trim(),
					recursive: true,
				}),
			});
			prefetchStatus = "[running] Загрузка запущена";
			watchDownload(
				(status) => (prefetchStatus = status),
				(logs) => (prefetchLog = logs),
				() => loadModules(),
			);
		} catch (e: any) {
			prefetchStatus = "Ошибка: " + e.message;
		}
	}
</script>

<div
	class="card bg-base-100/80 backdrop-blur-md shadow-2xl border border-base-content/10"
>
	<div class="card-body">
		<h3 class="card-title text-xl font-bold">Prefetch module@version</h3>
		<div class="form-control w-full">
			<label class="label" for="modpath"
				><span class="label-text opacity-70">Module path</span></label
			>
			<input
				id="modpath"
				type="text"
				placeholder="github.com/pkg/errors"
				class="input input-bordered w-full bg-base-200/50"
				bind:value={moduleInput}
				onblur={splitPastedVersion}
				disabled={isRunning}
			/>
		</div>
		<div class="form-control w-full">
			<label class="label" for="modvers"
				><span class="label-text opacity-70">Version (optional)</span></label
			>
			<input
				id="modvers"
				type="text"
				placeholder="v0.9.1 или пусто (= latest)"
				class="input input-bordered w-full bg-base-200/50"
				bind:value={versionInput}
				disabled={isRunning}
			/>
		</div>
		<div
			class="card-actions justify-start items-center mt-4 border-t border-base-content/10 pt-4"
		>
			<button
				class="btn btn-primary shadow-lg shadow-primary/20"
				onclick={startPrefetch}
				disabled={isRunning}>Скачать</button
			>
			{#if prefetchStatus.includes("[running]")}
				<button
					class="btn btn-error btn-outline shadow-lg shadow-error/20"
					onclick={cancelDownload}>Отмена</button
				>
			{/if}
			<span class="text-sm opacity-60 ml-2">{prefetchStatus}</span>
		</div>
		<div
			class="mt-4 bg-neutral/90 text-neutral-content p-4 rounded-xl h-40 overflow-y-auto font-mono text-xs whitespace-pre-wrap shadow-inner"
		>
			{#if prefetchLog.length > 0}
				{prefetchLog.join("\n")}
			{:else}
				<span class="opacity-50 italic">Логи появятся здесь.</span>
			{/if}
		</div>
	</div>
</div>
