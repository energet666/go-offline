package prefetch

import (
	"fmt"
	"sync"
	"time"
)

type Report struct {
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
}

type JobState struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	Message    string   `json:"message,omitempty"`
	Error      string   `json:"error,omitempty"`
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
	Logs       []string `json:"logs"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at,omitempty"`

	mu sync.Mutex
}

func (j *JobState) Logf(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.State == "queued" {
		j.State = "running"
	}
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	j.Logs = append(j.Logs, line)
	if len(j.Logs) > 300 {
		j.Logs = j.Logs[len(j.Logs)-300:]
	}
	j.Message = fmt.Sprintf("logs=%d", len(j.Logs))
}

func (j *JobState) Complete(report Report, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "done"
	j.Message = msg
	j.Downloaded = report.Downloaded
	j.Skipped = report.Skipped
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job done: "+msg)
}

func (j *JobState) Fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "error"
	j.Error = err.Error()
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job error: "+err.Error())
}

func (j *JobState) Snapshot() *JobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return &JobState{
		ID:         j.ID,
		Kind:       j.Kind,
		State:      j.State,
		Message:    j.Message,
		Error:      j.Error,
		StartedAt:  j.StartedAt,
		FinishedAt: j.FinishedAt,
		Downloaded: append([]string(nil), j.Downloaded...),
		Skipped:    append([]string(nil), j.Skipped...),
		Logs:       append([]string(nil), j.Logs...),
	}
}
