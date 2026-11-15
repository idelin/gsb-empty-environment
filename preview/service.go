package preview

import (
	"context"
	"fmt"

	"example.org/netsec/q-019/budget"
)

// Entry is one published attachment leaf ready for preview rendering.
type Entry struct {
	// Path is the root-to-leaf node path within the attachment tree.
	Path []string

	// Payload is the fully decoded leaf content.
	Payload []byte
}

// Store is the publication sink. Render is called at most once per successful
// Render call, only after every leaf decoded within budget, and never on a
// failed unfold.
type Store interface {
	Render(ctx context.Context, entries []Entry) error
}

// Service renders attachment previews under a fixed unfold budget.
type Service struct {
	limits budget.Limits
	store  Store
}

// NewService builds a Service. Invalid limits are reported here rather than
// failing later on the first request.
func NewService(limits budget.Limits, store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("preview: nil store: %w", budget.ErrInvalidConfig)
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Service{limits: limits, store: store}, nil
}

// Preview unfolds attachment under the service budget and publishes the
// rendered entries on full success. On any failure it returns a non-nil error,
// renders nothing and guarantees all staged buffers were reclaimed.
func (s *Service) Preview(ctx context.Context, attachment *budget.Node) ([]Entry, error) {
	result, err := budget.Unfold(ctx, s.limits, attachment)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, len(result.Leaves))
	for i, leaf := range result.Leaves {
		entries[i] = Entry{Path: leaf.Path, Payload: leaf.Data}
	}

	// Single publication point; only reached when the whole tree succeeded.
	if err := s.store.Render(ctx, entries); err != nil {
		return nil, fmt.Errorf("preview: render failed: %w", err)
	}
	return entries, nil
}
