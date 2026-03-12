package httphandlers

import "fmt"

func (s *Server) newBudget() *fetchBudget {
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
