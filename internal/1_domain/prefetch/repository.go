package prefetch

// JobRepository abstracts jobs state storage
type JobRepository interface {
	Create(kind string) *JobState
	Get(id string) (*JobState, bool)
	List() []*JobState
}
