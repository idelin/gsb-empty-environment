package leaserouter

import (
	"fmt"
	"time"
)

// Gateway is the client-facing entry point. It resolves the lease holder
// for a key through the router and forwards the request to that node.
type Gateway struct {
	router *Router
}

// NewGateway creates a gateway on top of a router.
func NewGateway(r *Router) *Gateway {
	return &Gateway{router: r}
}

// Query routes key to its lease holder and returns the stored value.
// It refuses to use an expired lease.
func (g *Gateway) Query(key string) (string, error) {
	lease, node, err := g.router.Route(key)
	if err != nil {
		return "", err
	}
	if lease.Expired(time.Now()) {
		return "", fmt.Errorf("lease #%d for %s held by %s has expired",
			lease.ID, lease.Range, lease.NodeID)
	}
	return node.Get(key)
}
