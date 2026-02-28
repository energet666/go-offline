<script lang="ts">
    import { CircleCheck } from "lucide-svelte";
    import { toastStore } from "../stores";
</script>

{#if $toastStore.show}
    <div class="toast toast-top toast-center z-100">
        <div
            class="alert alert-success shadow-lg text-success-content font-medium rounded-xl"
        >
            <CircleCheck class="shrink-0 h-6 w-6" />
            <span>{$toastStore.message}</span>
        </div>
    </div>
{/if}
