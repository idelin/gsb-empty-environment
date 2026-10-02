package leaseread

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var ErrNoQuorum = errors.New("leaseread: no quorum")

// Node 是集群中的一个副本节点。
type Node struct {
	ID    int
	Store *KVStore
}

// Transport 抽象节点间网络通信。
type Transport interface {
	Heartbeat(ctx context.Context, nodeID int) error
	Fetch(ctx context.Context, nodeID int, key string) (VersionedValue, bool, error)
	Replicate(ctx context.Context, nodeID int, key string, v VersionedValue) error
}

// SimTransport 用固定延迟模拟网络，并统计每类 RPC 的调用次数，
// 用于验证"租约窗口内读路径零网络开销"。
type SimTransport struct {
	nodes   map[int]*Node
	latency time.Duration

	HeartbeatCalls atomic.Int64
	FetchCalls     atomic.Int64
	ReplicateCalls atomic.Int64
}

func NewSimTransport(nodes map[int]*Node, latency time.Duration) *SimTransport {
	return &SimTransport{nodes: nodes, latency: latency}
}

func (t *SimTransport) HeartbeatCallsCount() int64 { return t.HeartbeatCalls.Load() }
func (t *SimTransport) FetchCallsCount() int64     { return t.FetchCalls.Load() }
func (t *SimTransport) ReplicateCallsCount() int64 { return t.ReplicateCalls.Load() }

// TotalCalls 返回读路径相关的网络调用总数（Heartbeat + Fetch）。
func (t *SimTransport) TotalCalls() int64 {
	return t.HeartbeatCalls.Load() + t.FetchCalls.Load()
}

func (t *SimTransport) simulate() {
	if t.latency > 0 {
		time.Sleep(t.latency)
	}
}

func (t *SimTransport) Heartbeat(ctx context.Context, nodeID int) error {
	t.HeartbeatCalls.Add(1)
	t.simulate()
	if _, ok := t.nodes[nodeID]; !ok {
		return errors.New("leaseread: unknown node")
	}
	return nil
}

func (t *SimTransport) Fetch(ctx context.Context, nodeID int, key string) (VersionedValue, bool, error) {
	t.FetchCalls.Add(1)
	t.simulate()
	n, ok := t.nodes[nodeID]
	if !ok {
		return VersionedValue{}, false, errors.New("leaseread: unknown node")
	}
	v, found := n.Store.Get(key)
	return v, found, nil
}

func (t *SimTransport) Replicate(ctx context.Context, nodeID int, key string, v VersionedValue) error {
	t.ReplicateCalls.Add(1)
	t.simulate()
	n, ok := t.nodes[nodeID]
	if !ok {
		return errors.New("leaseread: unknown node")
	}
	n.Store.Put(key, v)
	return nil
}
