package inmem_jobs

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go-offline/internal/1_domain/prefetch"
)

type repository struct {
	mu     sync.RWMutex
	jobs   map[string]*prefetch.JobState
	jobSeq uint64
}

// New creates a new in-memory job repository.
func New() prefetch.JobRepository {
	return &repository{
		jobs: make(map[string]*prefetch.JobState),
	}
}

func (r *repository) Create(kind string) *prefetch.JobState {
	id := fmt.Sprintf("j-%d", atomic.AddUint64(&r.jobSeq, 1))
	job := &prefetch.JobState{
		ID:        id,
		Kind:      kind,
		State:     "queued",
		StartedAt: time.Now().Format(time.RFC3339),
		Logs:      make([]string, 0, 64),
	}
	job.Logf("job created")

	r.mu.Lock()
	r.jobs[id] = job
	r.mu.Unlock()

	return job
}

func (r *repository) Get(id string) (*prefetch.JobState, bool) {
	r.mu.RLock()
	job, ok := r.jobs[id]
	r.mu.RUnlock()
	return job, ok
}

func (r *repository) List() []*prefetch.JobState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*prefetch.JobState, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	return out
}
