export async function copyText(text: string, event: Event) {
    try {
        await navigator.clipboard.writeText(text);
        const btn = event.target as HTMLButtonElement;
        if (btn) {
            const originalText = btn.innerText;
            btn.innerText = "Скопировано!";
            btn.classList.remove("btn-neutral");
            btn.classList.add("btn-success");
            setTimeout(() => {
                btn.innerText = originalText;
                btn.classList.remove("btn-success");
                btn.classList.add("btn-neutral");
            }, 2000);
        }
        return true;
    } catch (err) {
        console.error("Copy failed", err);
        return false;
    }
}

export async function fetchJSON(url: string, options?: RequestInit) {
    const res = await fetch(url, options);
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "request failed");
    return data;
}

export async function watchJob(
    jobId: string,
    onStatus: (status: string) => void,
    onLog: (logs: string[]) => void,
    onDone: () => void
) {
    let done = false;
    while (!done) {
        try {
            const job = await fetchJSON("/api/jobs/" + encodeURIComponent(jobId));
            const prefix = `[${job.state}] `;

            let statusText = "";
            if (job.state === "done") {
                statusText = prefix + (job.message || "Готово");
                done = true;
                onDone();
            } else if (job.state === "error") {
                statusText = prefix + (job.error || "Ошибка");
                done = true;
            } else {
                statusText = prefix + (job.message || "Выполняется...");
            }

            onStatus(statusText);
            onLog(job.logs || []);
        } catch (e: any) {
            onStatus("Ошибка статуса: " + e.message);
            done = true;
        }
        if (!done) {
            await new Promise((r) => setTimeout(r, 1000));
        }
    }
}
