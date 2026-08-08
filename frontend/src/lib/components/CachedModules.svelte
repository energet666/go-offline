<script lang="ts">
	import { onMount } from "svelte";
	import {
		Pin,
		PinOff,
		Copy,
		Check,
		Archive,
		ChevronRight,
		RefreshCw,
		ArrowUp,
		TriangleAlert,
	} from "lucide-svelte";
	import {
		modulesStore,
		modulesQueryStore,
		loadModules,
		pinModule,
		unpinModule,
		showToastMessage,
		checkUpdates,
		updatesStore,
		updatesCheckedAtStore,
		updatesLoadingStore,
		isDownloadingStore,
		type CachedModule,
		type ModuleUpdate,
	} from "../stores";
	import { copyToClipboard, fetchJSON, watchDownload } from "../utils";

	interface Props {
		proxyUrl: string;
	}

	let { proxyUrl }: Props = $props();

	let copiedRows = $state<Record<string, boolean>>({});
	const DEPS_STORAGE_KEY = "go-offline:deps-expanded";
	let depsExpanded = $state((() => {
		const stored = localStorage.getItem(DEPS_STORAGE_KEY);
		return stored === null ? false : stored === "true";
	})());

	function toggleDeps() {
		depsExpanded = !depsExpanded;
		localStorage.setItem(DEPS_STORAGE_KEY, String(depsExpanded));
	}

	// Split modules into pinned (user-requested) and dependencies
	let pinnedModules = $derived($modulesStore.filter((m) => m.pinned));
	let depModules = $derived($modulesStore.filter((m) => !m.pinned));

	async function copyGoGetCommand(module: string, version: string) {
		const ok = await copyToClipboard(`go get ${module}@${version}`);
		if (!ok) {
			showToastMessage("Не удалось скопировать в буфер обмена");
			return;
		}
		const key = `${module}@${version}`;
		copiedRows[key] = true;
		showToastMessage(`Скопировано: go get ${module}@${version}`);
		setTimeout(() => {
			copiedRows[key] = false;
		}, 1000);
	}

	async function handlePin(module: string, version: string) {
		try {
			await pinModule(module, version);
			showToastMessage(`${module}@${version} закреплён`);
		} catch {
			showToastMessage("Ошибка при закреплении пакета");
		}
	}

	async function handleUnpin(module: string, version: string) {
		try {
			await unpinModule(module, version);
			showToastMessage(`${module}@${version} убран из закреплённых`);
		} catch {
			showToastMessage("Ошибка при открепления пакета");
		}
	}

	let updatesError = $state("");
	// Считаем только те обновления, которые ещё не скачаны в кэш.
	let updatesAvailable = $derived(
		Object.values($updatesStore).filter(isActionable).length,
	);

	async function handleCheckUpdates() {
		updatesError = "";
		try {
			await checkUpdates(true);
		} catch (e: any) {
			updatesError = e.message;
			showToastMessage("Не удалось проверить обновления: " + e.message);
		}
	}

	// Обновление уже скачано, если версия из апстрима лежит в кэше.
	function isCached(module: string, version?: string) {
		if (!version) return false;
		return $modulesStore.some((m) => m.module === module && m.version === version);
	}

	function updateFor(row: CachedModule): ModuleUpdate | undefined {
		return $updatesStore[`${row.module}@${row.version}`];
	}

	function isActionable(u: ModuleUpdate) {
		const newerPending =
			u.has_update &&
			!!u.latest &&
			u.latest !== u.version &&
			!isCached(u.module, u.latest);
		const majorPending =
			!!u.next_major_module &&
			!!u.next_major_version &&
			!isCached(u.next_major_module, u.next_major_version);
		return newerPending || majorPending;
	}

	async function downloadVersion(module: string, version: string) {
		if ($isDownloadingStore) {
			showToastMessage("Дождитесь окончания текущей загрузки");
			return;
		}
		try {
			await fetchJSON("/api/prefetch", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ module, version, recursive: true }),
			});
			showToastMessage(`Загружается ${module}@${version}`);
			watchDownload(
				() => {},
				() => {},
				async () => {
					showToastMessage(`${module}@${version} загружен`);
					await loadModules();
					// Набор закреплённых изменился — отчёт пересоберётся сам.
					checkUpdates().catch(() => {});
				},
			);
		} catch (e: any) {
			showToastMessage("Ошибка загрузки: " + e.message);
		}
	}

	function formatDate(value?: string) {
		if (!value) return "";
		const d = new Date(value);
		return isNaN(d.getTime()) ? value : d.toLocaleDateString();
	}

	onMount(() => {
		loadModules($modulesQueryStore);
	});

	function handleSearch() {
		loadModules($modulesQueryStore);
	}

	function handleClear() {
		$modulesQueryStore = "";
		loadModules("");
	}
</script>

<div
	class="card bg-base-100/80 backdrop-blur-md shadow-2xl border border-base-content/10"
>
	<div class="card-body">
		<h3 class="card-title text-xl font-bold">Кэшированные модули</h3>
		<div class="my-2">
			<div class="badge badge-success gap-2 shadow-sm font-medium">
				GOPROXY={proxyUrl}
			</div>
		</div>
		<div class="flex gap-2 items-center mb-4">
			<input
				type="text"
				placeholder="Поиск по module/version"
				class="input input-bordered input-sm w-full max-w-xs bg-base-200/50"
				bind:value={$modulesQueryStore}
				oninput={handleSearch}
			/>
			<button class="btn btn-sm btn-outline opacity-80" onclick={handleClear}
				>Очистить</button
			>
		</div>

		<!-- Pinned / user-requested packages -->
		{#if pinnedModules.length > 0 || $modulesStore.length === 0}
			<div class="mb-1 flex items-center gap-2 flex-wrap">
				<Pin size={14} class="text-primary opacity-70" />
				<span class="text-sm font-semibold opacity-70 uppercase tracking-wider">
					Запрошенные пакеты
				</span>
				<span class="badge badge-primary badge-sm">{pinnedModules.length}</span>
				<div class="ml-auto flex items-center gap-2">
					{#if updatesError}
						<span class="text-xs text-error opacity-80">{updatesError}</span>
					{:else if $updatesCheckedAtStore}
						<span class="text-xs opacity-50">
							{updatesAvailable > 0
								? `Есть обновления: ${updatesAvailable}`
								: "Все версии актуальны"}
						</span>
					{/if}
					<button
						class="btn btn-xs btn-outline gap-1 opacity-80 hover:opacity-100"
						onclick={handleCheckUpdates}
						disabled={$updatesLoadingStore || pinnedModules.length === 0}
					>
						<RefreshCw
							size={12}
							class={$updatesLoadingStore ? "animate-spin" : ""}
						/>
						Проверить обновления
					</button>
				</div>
			</div>
			<div
				class="overflow-visible rounded-xl border border-primary/20 bg-primary/5 mb-4 [&_thead_th:first-child]:rounded-tl-[11px] [&_thead_th:last-child]:rounded-tr-[11px] [&_tbody_tr:last-child_td:first-child]:rounded-bl-[11px] [&_tbody_tr:last-child_td:last-child]:rounded-br-[11px]"
			>
				<table class="table table-sm w-full">
					<thead class="bg-primary/10 text-base-content/80">
						<tr>
							<th class="w-10"></th>
							<th class="w-[45%]">Module</th>
							<th>Version</th>
							<th>Time</th>
							<th class="w-16"></th>
						</tr>
					</thead>
					<tbody>
						{#each pinnedModules as row (row.module + "@" + row.version)}
							{@const key = `${row.module}@${row.version}`}
							{@const upd = updateFor(row)}
							<tr
								class="transition-colors hover:bg-base-content/5 {copiedRows[
									key
								]
									? 'bg-success/20!'
									: ''}"
							>
								<td class="text-center align-middle">
									{#if row.exported}
										<div
											class="inline-flex items-center justify-center text-success opacity-80 tooltip"
											data-tip="Экспортировано"
										>
											<Archive size={14} />
										</div>
									{:else}
										<div
											class="inline-flex items-center justify-center text-secondary opacity-70 tooltip"
											data-tip="Новый (не экспортирован)"
										>
											<div class="w-1.5 h-1.5 rounded-full bg-secondary animate-pulse shadow-[0_0_5px_rgba(var(--s),0.5)]"></div>
										</div>
									{/if}
								</td>
								<td class="break-all font-medium opacity-90">
									{row.module}
								</td>
								<td>
									<div class="flex items-center gap-1.5 flex-wrap">
										<div class="badge badge-primary badge-sm border-primary/20">
											{row.version}
										</div>
										{#if upd?.latest && upd.latest !== row.version && upd.has_update}
											{#if isCached(row.module, upd.latest)}
												<div
													class="badge badge-ghost badge-sm gap-1 tooltip whitespace-nowrap"
													data-tip="Новая версия уже в кэше"
												>
													<Check size={11} class="text-success" />
													{upd.latest}
												</div>
											{:else}
												<button
													class="badge badge-warning badge-sm gap-1 tooltip whitespace-nowrap cursor-pointer"
													data-tip="Доступна {upd.latest}{upd.published_at
														? ' от ' + formatDate(upd.published_at)
														: ''} — нажмите, чтобы скачать"
													disabled={$isDownloadingStore}
													onclick={(e) => {
														e.stopPropagation();
														downloadVersion(row.module, upd.latest!);
													}}
												>
													<ArrowUp size={11} />
													{upd.latest}
												</button>
											{/if}
										{/if}
										{#if upd?.next_major_module && upd.next_major_version}
											{#if isCached(upd.next_major_module, upd.next_major_version)}
												<div
													class="badge badge-ghost badge-sm gap-1 tooltip whitespace-nowrap"
													data-tip="Мажор {upd.next_major_module} уже в кэше"
												>
													<Check size={11} class="text-success" />
													{upd.next_major_version}
												</div>
											{:else}
												<button
													class="badge badge-secondary badge-sm gap-1 tooltip whitespace-nowrap cursor-pointer"
													data-tip="Новый мажор: {upd.next_major_module}@{upd.next_major_version} — нажмите, чтобы скачать"
													disabled={$isDownloadingStore}
													onclick={(e) => {
														e.stopPropagation();
														downloadVersion(
															upd.next_major_module!,
															upd.next_major_version!,
														);
													}}
												>
													<ArrowUp size={11} />
													{upd.next_major_version}
												</button>
											{/if}
										{/if}
										{#if upd?.error}
											<span
												class="opacity-40 tooltip inline-flex items-center"
												data-tip="Проверка не удалась: {upd.error}"
											>
												<TriangleAlert size={12} />
											</span>
										{/if}
									</div>
								</td>
								<td class="text-xs opacity-60">
									{row.time || ""}
								</td>
								<td>
									<div class="flex gap-1 justify-end">
										<button
											class="btn btn-ghost btn-xs opacity-60 hover:opacity-100 tooltip"
											data-tip="Скопировать go get"
											onclick={(e) => {
												e.stopPropagation();
												copyGoGetCommand(row.module, row.version);
											}}
										>
											{#if copiedRows[key]}
												<Check size={13} class="text-success" />
											{:else}
												<Copy size={13} />
											{/if}
										</button>
										<button
											class="btn btn-ghost btn-xs opacity-50 hover:opacity-100 hover:text-warning tooltip"
											data-tip="Убрать из закреплённых"
											onclick={(e) => {
												e.stopPropagation();
												handleUnpin(row.module, row.version);
											}}
										>
											<PinOff size={13} />
										</button>
									</div>
								</td>
							</tr>
						{/each}
						{#if pinnedModules.length === 0}
							<tr>
								<td
									colspan="5"
									class="text-center py-4 opacity-40 italic text-sm"
									>Нет закреплённых пакетов</td
								>
							</tr>
						{/if}
					</tbody>
				</table>
			</div>
		{/if}

		<!-- Transitive dependencies -->
		{#if depModules.length > 0}
			<button
				class="mb-1 flex items-center gap-2 cursor-pointer select-none group w-full text-left"
				onclick={toggleDeps}
			>
				<span
					class="transition-transform duration-200 opacity-50 group-hover:opacity-80"
					class:rotate-90={depsExpanded}
				>
					<ChevronRight size={14} />
				</span>
				<span class="text-sm font-semibold opacity-70 uppercase tracking-wider group-hover:opacity-90 transition-opacity">
					Транзитивные зависимости
				</span>
				<span class="badge badge-ghost badge-sm">{depModules.length}</span>
			</button>
			{#if depsExpanded}
				<div
					class="overflow-visible rounded-xl border border-base-content/5 bg-base-200/30 [&_thead_th:first-child]:rounded-tl-[11px] [&_thead_th:last-child]:rounded-tr-[11px] [&_tbody_tr:last-child_td:first-child]:rounded-bl-[11px] [&_tbody_tr:last-child_td:last-child]:rounded-br-[11px]"
				>
					<table class="table table-sm w-full">
						<thead class="bg-base-300/50 text-base-content/80">
							<tr>
								<th class="w-10"></th>
								<th>Module</th>
								<th>Version</th>
								<th>Time</th>
								<th class="w-16"></th>
							</tr>
						</thead>
						<tbody>
							{#each depModules as row (row.module + "@" + row.version)}
								{@const key = `${row.module}@${row.version}`}
								<tr
									class="transition-colors hover:bg-base-content/5 {copiedRows[
										key
									]
										? 'bg-success/20!'
										: ''}"
								>
									<td class="text-center align-middle">
										{#if row.exported}
											<div
												class="inline-flex items-center justify-center text-success opacity-80 tooltip"
												data-tip="Экспортировано"
											>
												<Archive size={14} />
											</div>
										{:else}
											<div
												class="inline-flex items-center justify-center text-secondary opacity-70 tooltip"
												data-tip="Новый (не экспортирован)"
											>
												<div
													class="w-1.5 h-1.5 rounded-full bg-secondary animate-pulse shadow-[0_0_5px_rgba(var(--s),0.5)]"
												></div>
											</div>
										{/if}
									</td>
									<td class="break-all font-medium opacity-90">
										{row.module}
									</td>
									<td>
										<div
											class="badge badge-ghost badge-sm border-base-content/10"
										>
											{row.version}
										</div>
									</td>
									<td class="text-xs opacity-60">
										{row.time || ""}
									</td>
									<td>
										<div class="flex gap-1 justify-end">
											<button
												class="btn btn-ghost btn-xs opacity-40 hover:opacity-80 tooltip"
												data-tip="Скопировать go get"
												onclick={(e) => {
													e.stopPropagation();
													copyGoGetCommand(row.module, row.version);
												}}
											>
												{#if copiedRows[key]}
													<Check size={13} class="text-success" />
												{:else}
													<Copy size={13} />
												{/if}
											</button>
											<button
												class="btn btn-ghost btn-xs opacity-40 hover:opacity-80 hover:text-primary tooltip"
												data-tip="Закрепить"
												onclick={(e) => {
													e.stopPropagation();
													handlePin(row.module, row.version);
												}}
											>
												<Pin size={13} />
											</button>
										</div>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		{/if}

		{#if $modulesStore.length === 0}
			<div class="text-center py-6 opacity-50 italic">Ничего не найдено</div>
		{/if}
	</div>
</div>
