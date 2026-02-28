<script lang="ts">
    import { copyText } from "../utils";
    export let proxyUrl: string;
</script>

<div
    class="bg-base-200/50 backdrop-blur-md border border-base-content/10 shadow-sm rounded-2xl p-5 mb-8"
>
    <h3 class="font-bold text-xl mb-3 text-primary flex items-center gap-2">
        Как подключить Go к этому прокси
    </h3>
    <p class="text-sm mb-2 opacity-80">Установить переменные:</p>
    <div class="relative mb-4 group">
        <pre
            class="bg-neutral text-success p-3 pl-4 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -w GOPROXY={proxyUrl} GOSUMDB=off</pre>
        <button
            class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-xs btn-primary opacity-0 group-hover:opacity-100 transition-opacity w-[100px]"
            onclick={(e) =>
                copyText(`go env -w GOPROXY=${proxyUrl} GOSUMDB=off`, e)}
            >Копировать</button
        >
    </div>
    <p class="text-sm mb-2 opacity-80">
        Вернуть по умолчанию (отключить прокси):
    </p>
    <div class="relative group">
        <pre
            class="bg-neutral text-success p-3 pl-4 rounded-xl text-sm overflow-x-auto font-mono shadow-inner border border-neutral-focus">go env -u GOPROXY GOSUMDB</pre>
        <button
            class="absolute top-1/2 -translate-y-1/2 right-2 btn btn-xs btn-primary opacity-0 group-hover:opacity-100 transition-opacity w-[100px]"
            onclick={(e) => copyText("go env -u GOPROXY GOSUMDB", e)}
            >Копировать</button
        >
    </div>
</div>
