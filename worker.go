package leasecoord

import "fmt"

// WorkerNode is an edge worker holding a fine-grained sub-lease from its
// regional gateway. It never contacts the root coordinator.
type WorkerNode struct {
	ID      string
	gateway *RegionGateway
	quota   int64
	lease   Lease
	held    bool
}

func NewWorkerNode(id string, gateway *RegionGateway) *WorkerNode {
	return &WorkerNode{ID: id, gateway: gateway}
}

// Acquire obtains a sub-lease for the given quota from the local gateway.
func (w *WorkerNode) Acquire(quota int64) error {
	l, err := w.gateway.RequestSubLease(w.ID, quota)
	if err != nil {
		return err
	}
	w.lease = l
	w.quota = quota
	w.held = true
	return nil
}

// Renew extends the current sub-lease before it expires.
func (w *WorkerNode) Renew() error {
	if !w.held {
		return fmt.Errorf("worker %q holds no lease", w.ID)
	}
	l, err := w.gateway.RequestSubLease(w.ID, w.quota)
	if err != nil {
		return err
	}
	w.lease = l
	return nil
}

// Release returns the sub-lease to the gateway immediately.
func (w *WorkerNode) Release() {
	w.gateway.ReleaseSubLease(w.ID)
	w.held = false
}

// Held reports whether the worker currently holds a sub-lease.
func (w *WorkerNode) Held() bool { return w.held }
