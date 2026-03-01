package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

func (s *server) newBudget() *fetchBudget {
	return &fetchBudget{
		maxBytes:   s.maxJobBytes,
		maxModules: s.maxModules,
	}
}

func (b *fetchBudget) noteModule() error {
	b.modules++
	if b.maxModules > 0 && b.modules > b.maxModules {
		return fmt.Errorf("job limit reached: modules=%d > max=%d", b.modules, b.maxModules)
	}
	return nil
}

func (b *fetchBudget) noteBytes(n int64) error {
	b.bytes += n
	if b.maxBytes > 0 && b.bytes > b.maxBytes {
		return fmt.Errorf("job limit reached: downloaded=%s > max=%s", humanBytes(b.bytes), humanBytes(b.maxBytes))
	}
	return nil
}

func (s *server) newJob(kind string) *jobState {
	id := fmt.Sprintf("j-%d", atomic.AddUint64(&s.jobSeq, 1))
	job := &jobState{
		ID:        id,
		Kind:      kind,
		State:     "queued",
		StartedAt: time.Now().Format(time.RFC3339),
		Logs:      make([]string, 0, 64),
	}
	job.logf("job created")
	s.jobsMu.Lock()
	s.jobs[id] = job
	s.jobsMu.Unlock()
	return job
}

func (s *server) getJob(id string) (*jobState, bool) {
	s.jobsMu.RLock()
	job, ok := s.jobs[id]
	s.jobsMu.RUnlock()
	return job, ok
}

func (j *jobState) logf(format string, args ...any) {
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

func (j *jobState) complete(report prefetchReport, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "done"
	j.Message = msg
	j.Downloaded = report.Downloaded
	j.Skipped = report.Skipped
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job done: "+msg)
}

func (j *jobState) fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "error"
	j.Error = err.Error()
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job error: "+err.Error())
}

func (j *jobState) snapshot() *jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return &jobState{
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
