package saga

import (
	"fmt"
	"sync"
	"time"
)

// world simulates the external systems around the engine and records
// every interaction so tests can assert "no duplicate, no loss".
type world struct {
	eng *Engine // current engine generation (swapped after "power loss")
	mu  sync.Mutex

	approveRequests  []string
	approverNotified []string
	purchaseCalls    int
	orders           map[string]string // idempotency key -> order id
	canceled         []string
	bankSubmissions  []string
	bankResponds     bool
	crashOnPurchase  bool // simulate power loss right after the external effect
}

func newWorld() *world {
	return &world{orders: make(map[string]string)}
}

func (w *world) definition(payTimeout time.Duration) *Definition {
	return &Definition{
		Name: "procurement",
		Steps: []Step{
			{
				Name:    "approve",
				Timeout: 5 * time.Second,
				Action: func(ctx *StepContext) (interface{}, error) {
					w.mu.Lock()
					w.approveRequests = append(w.approveRequests, ctx.IdempotencyKey)
					w.mu.Unlock()
					go func() {
						time.Sleep(20 * time.Millisecond)
						w.eng.Deliver(ctx.TxID, "APPROVED")
					}()
					return nil, ErrAwait
				},
				Compensate: func(ctx *StepContext) error {
					w.mu.Lock()
					w.approverNotified = append(w.approverNotified, ctx.IdempotencyKey)
					w.mu.Unlock()
					return nil
				},
			},
			{
				Name: "purchase",
				Action: func(ctx *StepContext) (interface{}, error) {
					w.mu.Lock()
					w.purchaseCalls++
					id, dup := w.orders[ctx.IdempotencyKey]
					if !dup {
						id = fmt.Sprintf("PO-%04d", len(w.orders)+1)
						w.orders[ctx.IdempotencyKey] = id
					}
					crash := w.crashOnPurchase
					w.mu.Unlock()
					ctx.Data["order_id"] = id
					if crash {
						// Power loss after the external effect but
						// before StepCompleted reaches the journal.
						panic(errCrash)
					}
					return id, nil
				},
				Compensate: func(ctx *StepContext) error {
					w.mu.Lock()
					defer w.mu.Unlock()
					if id, ok := w.orders[ctx.IdempotencyKey]; ok {
						delete(w.orders, ctx.IdempotencyKey)
						w.canceled = append(w.canceled, id)
					}
					return nil
				},
			},
			{
				Name:    "pay",
				Timeout: payTimeout,
				Action: func(ctx *StepContext) (interface{}, error) {
					w.mu.Lock()
					w.bankSubmissions = append(w.bankSubmissions, ctx.IdempotencyKey)
					respond := w.bankResponds
					w.mu.Unlock()
					if respond {
						go func() {
							time.Sleep(20 * time.Millisecond)
							w.eng.Deliver(ctx.TxID, "PAID")
						}()
					}
					return nil, ErrAwait
				},
			},
		},
	}
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func indexOf(events []Event, typ EventType, step string) int {
	for i, ev := range events {
		if ev.Type == typ && (step == "" || ev.Step == step) {
			return i
		}
	}
	return -1
}
