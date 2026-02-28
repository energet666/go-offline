<script lang="ts">
    import { onMount } from "svelte";
    import {
        modulesStore,
        modulesQueryStore,
        loadModules,
        showToastMessage,
    } from "../stores";

    export let proxyUrl: string;

    let copiedRows: Record<string, boolean> = {};

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

<div class="card bg-base-100 shadow-xl border border-base-200">
    <div class="card-body">
        <h3 class="card-title text-xl">Кэшированные модули</h3>
        <div class="my-2">
            <div class="badge badge-success gap-2">GOPROXY={proxyUrl}</div>
        </div>
        <div class="flex gap-2 items-center mb-4">
            <input
                type="text"
                placeholder="Поиск по module/version"
                class="input input-bordered input-sm w-full max-w-xs"
                bind:value={$modulesQueryStore}
                oninput={handleSearch}
            />
            <button class="btn btn-sm btn-outline" onclick={handleClear}
                >Очистить</button
            >
        </div>
        <div class="overflow-x-auto">
            <table class="table table-sm w-full">
                <thead>
                    <tr>
                        <th>Module</th>
                        <th>Version</th>
                        <th>Time</th>
                    </tr>
                </thead>
                <tbody>
                    {#each $modulesStore as row}
                        <tr
                            class="cursor-pointer transition-colors hover:bg-base-200 {copiedRows[
                                `${row.module}@${row.version}`
                            ]
                                ? 'bg-success/20!'
                                : ''}"
                            title="Нажмите, чтобы скопировать команду go get"
                            onclick={() =>
                                copyGoGetCommand(row.module, row.version)}
                        >
                            <td class="break-all">{row.module}</td>
                            <td
                                ><div class="badge badge-ghost badge-sm">
                                    {row.version}
                                </div></td
                            >
                            <td class="text-xs text-slate-500"
                                >{row.time || ""}</td
                            >
                        </tr>
                    {/each}
                    {#if $modulesStore.length === 0}
                        <tr>
                            <td
                                colspan="3"
                                class="text-center py-4 text-slate-500"
                                >Ничего не найдено</td
                            >
                        </tr>
                    {/if}
                </tbody>
            </table>
        </div>
    </div>
</div>
