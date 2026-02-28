<script lang="ts">
    import { copyText } from "../utils";
    export let proxyUrl: string;
</script>

<div class="bg-primary/10 border border-primary/20 rounded-xl p-4 mb-6">
    <h3 class="font-semibold text-lg mb-2 text-primary">
        Как подключить Go к этому прокси
    </h3>
    <p class="text-sm mb-2">Установить переменные:</p>
    <div class="relative mb-3">
        <pre
            class="bg-slate-900 text-green-300 p-3 rounded-lg text-sm overflow-x-auto">go env -w GOPROXY={proxyUrl} GOSUMDB=off</pre>
        <button
            class="absolute top-2 right-2 btn btn-xs btn-neutral w-[100px]"
            onclick={(e) =>
                copyText(`go env -w GOPROXY=${proxyUrl} GOSUMDB=off`, e)}
            >Копировать</button
        >
    </div>
    <p class="text-sm mb-2">Вернуть по умолчанию (отключить прокси):</p>
    <div class="relative">
        <pre
            class="bg-slate-900 text-green-300 p-3 rounded-lg text-sm overflow-x-auto">go env -u GOPROXY GOSUMDB</pre>
        <button
            class="absolute top-2 right-2 btn btn-xs btn-neutral w-[100px]"
            onclick={(e) => copyText("go env -u GOPROXY GOSUMDB", e)}
            >Копировать</button
        >
    </div>
</div>
