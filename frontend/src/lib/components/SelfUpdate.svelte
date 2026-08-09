<script lang="ts">
	import { RefreshCw, Loader2, ArrowUpCircle, CheckCircle2 } from "lucide-svelte";
	import { fetchJSON } from "../utils";
	import { isDownloadingStore, showToastMessage, type SelfUpdateStatus } from "../stores";

	// Проверка ходит в интернет, поэтому запускается только по кнопке:
	// приложение штатно живёт в офлайн-среде.
	let status = $state<SelfUpdateStatus | null>(null);
	let checking = $state(false);
	let updating = $state(false);
	let phase = $state("");
	let error = $state("");
	let current = $state<{ version: string; built_at?: string } | null>(null);

	// Версия — это commit sha, показываем привычные семь символов.
	function short(v?: string) {
		if (!v) return "—";
		return v === "dev" ? "dev" : v.slice(0, 7);
	}

	function formatDate(iso?: string) {
		if (!iso || iso === "unknown") return "";
		const d = new Date(iso);
		return isNaN(d.getTime()) ? iso : d.toLocaleString();
	}

	const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

	async function loadCurrent() {
		try {
			current = await fetchJSON("/api/version");
		} catch {
			// Версия — не критичная информация, молча оставляем прочерк.
		}
	}
	loadCurrent();

	async function check() {
		checking = true;
		error = "";
		try {
			status = await fetchJSON("/api/self-update/check");
			current = status!.current;
		} catch (e: any) {
			error = e.message;
			status = null;
		} finally {
			checking = false;
		}
	}

	async function apply() {
		if ($isDownloadingStore) {
			showToastMessage("Дождитесь окончания текущей загрузки");
			return;
		}
		updating = true;
		error = "";
		phase = "Запуск обновления…";
		const before = current?.version;

		try {
			await fetchJSON("/api/self-update/apply", { method: "POST" });
		} catch (e: any) {
			error = e.message;
			updating = false;
			return;
		}

		// Фаза 1: следим за фоновой задачей, пока сервер ещё отвечает.
		while (true) {
			await sleep(1000);
			let state: any;
			try {
				state = await fetchJSON("/api/download-status");
			} catch {
				// Сервер уже уходит на перезапуск.
				break;
			}
			if (state.status === "error") {
				error = state.error || "Обновление не удалось";
				updating = false;
				return;
			}
			phase = state.logs?.length ? state.logs[state.logs.length - 1] : "Скачивание…";
			if (state.status === "done") break;
		}

		// Фаза 2: ждём, пока поднимется новая версия.
		phase = "Перезапуск сервера…";
		for (let i = 0; i < 60; i++) {
			await sleep(1000);
			try {
				const v = await fetchJSON("/api/version");
				if (v.version && v.version !== before) {
					phase = "Обновлено, перезагружаю страницу…";
					await sleep(800);
					window.location.reload();
					return;
				}
			} catch {
				// Сервер ещё не поднялся — продолжаем ждать.
			}
		}
		error = "Сервер не ответил после перезапуска. Проверьте консоль процесса.";
		updating = false;
	}
</script>

<div
	class="card bg-base-100/80 backdrop-blur-md shadow-2xl border border-base-content/10 mb-8"
>
	<div class="card-body">
		<div class="flex flex-wrap items-center justify-between gap-4">
			<div>
				<h3 class="card-title text-xl font-bold">Обновление приложения</h3>
				<p class="text-sm opacity-70 mt-1">
					Текущая версия
					<span class="font-mono">{short(current?.version)}</span>
					{#if formatDate(current?.built_at)}
						· собрана {formatDate(current?.built_at)}
					{/if}
				</p>
			</div>
			<div class="flex gap-2">
				<button
					class="btn btn-outline btn-sm gap-2"
					onclick={check}
					disabled={checking || updating}
				>
					{#if checking}
						<Loader2 size={16} class="animate-spin" />
						Проверяю…
					{:else}
						<RefreshCw size={16} />
						Проверить новую версию
					{/if}
				</button>
				{#if status?.can_update}
					<button
						class="btn btn-primary btn-sm gap-2 shadow-lg shadow-primary/20"
						onclick={apply}
						disabled={updating}
					>
						{#if updating}
							<Loader2 size={16} class="animate-spin" />
							Обновление…
						{:else}
							<ArrowUpCircle size={16} />
							Обновить
						{/if}
					</button>
				{/if}
			</div>
		</div>

		{#if error}
			<div class="alert alert-error mt-4 py-2 text-sm">{error}</div>
		{:else if updating}
			<div class="mt-4 text-sm opacity-70 font-mono">{phase}</div>
		{:else if status}
			<div class="mt-4 text-sm">
				{#if status.has_update}
					<div class="flex items-center gap-2">
						<ArrowUpCircle size={16} class="text-primary" />
						<span>
							Доступна версия
							<span class="font-mono font-bold">{short(status.latest?.version)}</span>
							{#if formatDate(status.latest?.built_at)}
								от {formatDate(status.latest?.built_at)}
							{/if}
						</span>
					</div>
				{:else}
					<div class="flex items-center gap-2">
						<CheckCircle2 size={16} class="text-success" />
						<span>Установлена последняя версия</span>
					</div>
				{/if}
				{#if status.reason}
					<p class="opacity-60 mt-1">{status.reason}</p>
				{/if}
			</div>
		{/if}
	</div>
</div>
