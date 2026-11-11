package mailfrag

import "fmt"

// Queue is the sender-side recipient queue. Implementations must treat
// Enqueue as the single hand-off point for a complete HTML body.
type Queue interface {
	Enqueue(to, htmlBody string) error
}

// EnqueueRendered renders n and enqueues the result for recipient to.
// The fragment is rendered in full before the queue is touched: if
// rendering fails, nothing is enqueued and no partial HTML can leak into
// the outbound stream.
func EnqueueRendered(q Queue, to string, n Node) error {
	html, err := Render(n)
	if err != nil {
		return fmt.Errorf("mailfrag: not enqueued for %q: %w", to, err)
	}
	if err := q.Enqueue(to, html); err != nil {
		return fmt.Errorf("mailfrag: enqueue for %q: %w", to, err)
	}
	return nil
}
