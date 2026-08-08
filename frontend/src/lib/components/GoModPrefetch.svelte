<script lang="ts">
	import { cancelDownload, fetchJSON, watchDownload } from "../utils";
	import { loadModules, isDownloadingStore } from "../stores";

	let gomodInput = $state("");
	let gomodStatus = $state("");
	let gomodLog = $state<string[]>([]);
	let isRunning = $derived($isDownloadingStore || gomodStatus.includes("[running]"));
	let isDragOver = $state(false);
	let dropError = $state("");

	const MAX_GOMOD_SIZE = 1024 * 1024;

	function hasFiles(e: DragEvent) {
		return Array.from(e.dataTransfer?.types ?? []).includes("Files");
	}

	function onDragOver(e: DragEvent) {
		// Перетаскивание обычного текста оставляем браузеру.
		if (isRunning || !hasFiles(e)) return;
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
		isDragOver = true;
	}

	function onDragLeave(e: DragEvent) {
		const next = e.relatedTarget as Node | null;
		if (next && (e.currentTarget as HTMLElement).contains(next)) return;
		isDragOver = false;
	}

	async function onDrop(e: DragEvent) {
		if (isRunning || !hasFiles(e)) return;
		e.preventDefault();
		isDragOver = false;
		dropError = "";
		const file = e.dataTransfer?.files?.[0];
		if (!file) return;
		if (file.size > MAX_GOMOD_SIZE) {
			dropError = `Файл слишком большой (${Math.round(file.size / 1024)} КБ), это вряд ли go.mod`;
			return;
		}
		try {
			gomodInput = await file.text();
			if (!/^\s*(module|go|require|replace|exclude|retract)\b/m.test(gomodInput)) {
				dropError = `«${file.name}» не похож на go.mod — проверьте содержимое`;
			}
		} catch (err: any) {
			dropError = "Не удалось прочитать файл: " + err.message;
		}
	}

	async function onFilePick(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = "";
		if (!file) return;
		dropError = "";
		try {
			gomodInput = await file.text();
		} catch (err: any) {
			dropError = "Не удалось прочитать файл: " + err.message;
		}
	}

	async function startGomodPrefetch() {
		if ($isDownloadingStore) return;
		gomodStatus = "Запуск загрузки...";
		gomodLog = ["Запуск..."];
		try {
			await fetchJSON("/api/prefetch-gomod", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					gomod: gomodInput,
					recursive: true,
				}),
			});
			gomodStatus = "[running] Загрузка запущена";
			watchDownload(
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
			<label class="label flex-wrap gap-2" for="gomodtext">
				<span class="label-text opacity-70"
					>Вставьте содержимое go.mod или перетащите файл сюда</span
				>
				<span class="label-text-alt">
					<label class="link link-primary text-xs cursor-pointer">
						выбрать файл
						<input
							type="file"
							class="hidden"
							onchange={onFilePick}
							disabled={isRunning}
						/>
					</label>
				</span>
			</label>
			<div
				class="relative w-full"
				role="presentation"
				ondragover={onDragOver}
				ondragleave={onDragLeave}
				ondrop={onDrop}
			>
				<textarea
					id="gomodtext"
					class="textarea textarea-bordered w-full h-28 font-mono bg-base-200/50 transition-colors {isDragOver
						? 'border-primary border-dashed'
						: ''}"
					placeholder="module your/module&#10;go 1.22&#10;require github.com/pkg/errors v0.9.1"
					bind:value={gomodInput}
					disabled={isRunning}
				></textarea>
				{#if isDragOver}
					<div
						class="absolute inset-0 rounded-box bg-primary/10 border-2 border-dashed border-primary flex items-center justify-center pointer-events-none"
					>
						<span class="text-sm font-medium text-primary">Отпустите go.mod</span
						>
					</div>
				{/if}
			</div>
			{#if dropError}
				<span class="label-text-alt text-warning mt-1">{dropError}</span>
			{/if}
		</div>
		<div
			class="card-actions justify-start items-center mt-4 border-t border-base-content/10 pt-4"
		>
			<button
				class="btn btn-primary shadow-lg shadow-primary/20"
				onclick={startGomodPrefetch}
				disabled={isRunning}>Скачать зависимости</button
			>
			{#if gomodStatus.includes("[running]")}
				<button
					class="btn btn-error btn-outline shadow-lg shadow-error/20"
					onclick={cancelDownload}>Отмена</button
				>
			{/if}
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
