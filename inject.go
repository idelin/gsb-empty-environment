package bptree

// InjectRoutingKey overwrites the routing key n.keys[i]. It exists solely
// to simulate on-disk corruption so that demos and tests can verify that
// Inspect catches and localizes routing-key violations. Never use it in
// production code paths.
func InjectRoutingKey(n *Node, i, v int) {
	n.keys[i] = v
}
