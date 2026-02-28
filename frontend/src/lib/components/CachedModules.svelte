<script lang="ts">
    import { onMount } from "svelte";
    import { Pin, PinOff, Copy, Check, Archive } from "lucide-svelte";
    import {
        modulesStore,
        modulesQueryStore,
        loadModules,
        unpinModule,
        showToastMessage,
        type CachedModule,
    } from "../stores";

    export let proxyUrl: string;

    let copiedRows: Record<string, boolean> = {};

    // Split modules into pinned (user-requested) and dependencies
    $: pinnedModules = $modulesStore.filter((m) => m.pinned);
    $: depModules = $modulesStore.filter((m) => !m.pinned);

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

    async function handleUnpin(module: string, version: string) {
        try {
            await unpinModule(module, version);
            showToastMessage(`${module}@${version} убран из закреплённых`);
        } catch {
            showToastMessage("Ошибка при открепления пакета");
        }
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
            <button
                class="btn btn-sm btn-outline opacity-80"
                onclick={handleClear}>Очистить</button
            >
        </div>

        <!-- Pinned / user-requested packages -->
        {#if pinnedModules.length > 0 || $modulesStore.length === 0}
            <div class="mb-1 flex items-center gap-2">
                <Pin size={14} class="text-primary opacity-70" />
                <span
                    class="text-sm font-semibold opacity-70 uppercase tracking-wider"
                >
                    Запрошенные пакеты
                </span>
                <span class="badge badge-primary badge-sm"
                    >{pinnedModules.length}</span
                >
            </div>
            <div
                class="overflow-visible rounded-xl border border-primary/20 bg-primary/5 mb-4"
            >
                <table class="table table-sm w-full">
                    <thead class="bg-primary/10 text-base-content/80">
                        <tr>
                            <th>Module</th>
                            <th>Version</th>
                            <th>Time</th>
                            <th class="w-16"></th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each pinnedModules as row (row.module + "@" + row.version)}
                            {@const key = `${row.module}@${row.version}`}
                            <tr
                                class="transition-colors hover:bg-base-content/5 {copiedRows[
                                    key
                                ]
                                    ? 'bg-success/20!'
                                    : ''}"
                            >
                                <td class="break-all font-medium opacity-90"
                                    >{row.module}</td
                                >
                                <td
                                    ><div
                                        class="badge badge-primary badge-sm border-primary/20"
                                    >
                                        {row.version}
                                    </div></td
                                >
                                <td class="text-xs opacity-60">
                                    {row.time || ""}
                                    {#if row.exported}
                                        <span
                                            class="inline-flex items-center ml-2 text-success opacity-80 tooltip"
                                            data-tip="Экспортировано"
                                        >
                                            <Archive size={13} />
                                        </span>
                                    {/if}
                                </td>
                                <td>
                                    <div class="flex gap-1 justify-end">
                                        <button
                                            class="btn btn-ghost btn-xs opacity-60 hover:opacity-100 tooltip"
                                            data-tip="Скопировать go get"
                                            onclick={(e) => {
                                                e.stopPropagation();
                                                copyGoGetCommand(
                                                    row.module,
                                                    row.version,
                                                );
                                            }}
                                        >
                                            {#if copiedRows[key]}
                                                <Check
                                                    size={13}
                                                    class="text-success"
                                                />
                                            {:else}
                                                <Copy size={13} />
                                            {/if}
                                        </button>
                                        <button
                                            class="btn btn-ghost btn-xs opacity-50 hover:opacity-100 hover:text-warning tooltip"
                                            data-tip="Убрать из закреплённых"
                                            onclick={(e) => {
                                                e.stopPropagation();
                                                handleUnpin(
                                                    row.module,
                                                    row.version,
                                                );
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
                                    colspan="4"
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
            <div class="mb-1 flex items-center gap-2">
                <span class="opacity-40 text-xs">⬡</span>
                <span
                    class="text-sm font-semibold opacity-70 uppercase tracking-wider"
                >
                    Транзитивные зависимости
                </span>
                <span class="badge badge-ghost badge-sm"
                    >{depModules.length}</span
                >
            </div>
            <div
                class="overflow-visible rounded-xl border border-base-content/5 bg-base-200/30"
            >
                <table class="table table-sm w-full">
                    <thead class="bg-base-300/50 text-base-content/80">
                        <tr>
                            <th>Module</th>
                            <th>Version</th>
                            <th>Time</th>
                            <th class="w-10"></th>
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
                                <td class="break-all font-medium opacity-90"
                                    >{row.module}</td
                                >
                                <td
                                    ><div
                                        class="badge badge-ghost badge-sm border-base-content/10"
                                    >
                                        {row.version}
                                    </div></td
                                >
                                <td class="text-xs opacity-60">
                                    {row.time || ""}
                                    {#if row.exported}
                                        <span
                                            class="inline-flex items-center ml-2 text-success opacity-80 tooltip"
                                            data-tip="Экспортировано"
                                        >
                                            <Archive size={13} />
                                        </span>
                                    {/if}
                                </td>
                                <td>
                                    <button
                                        class="btn btn-ghost btn-xs opacity-40 hover:opacity-80 tooltip"
                                        data-tip="Скопировать go get"
                                        onclick={(e) => {
                                            e.stopPropagation();
                                            copyGoGetCommand(
                                                row.module,
                                                row.version,
                                            );
                                        }}
                                    >
                                        {#if copiedRows[key]}
                                            <Check
                                                size={13}
                                                class="text-success"
                                            />
                                        {:else}
                                            <Copy size={13} />
                                        {/if}
                                    </button>
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        {/if}

        {#if $modulesStore.length === 0}
            <div class="text-center py-6 opacity-50 italic">
                Ничего не найдено
            </div>
        {/if}
    </div>
</div>
