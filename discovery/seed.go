package discovery

import (
	"context"
	"net/http"
	"time"
)

// Seed 是静态种子节点：持有权威成员表，应答新节点的 Bootstrap 握手，
// 同时参与 Gossip 反熵交换，把新成员扩散到全网。
type Seed struct {
	store *Store
	srv   *http.Server
	Addr  string
}

func NewSeed() *Seed {
	return &Seed{store: NewStore()}
}

// Start 在随机空闲端口启动 Seed 服务。
func (s *Seed) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/gossip", s.handleGossip)
	srv, addr, err := serve(mux)
	if err != nil {
		return err
	}
	s.srv = srv
	s.Addr = addr
	return nil
}

// Register 向 Seed 权威成员表登记一个成员（供集群初始成员或测试预置）。
func (s *Seed) Register(m NodeMeta) {
	s.store.Upsert(m)
}

// Members 返回 Seed 当前成员快照。
func (s *Seed) Members() []NodeMeta {
	return s.store.Snapshot()
}

func (s *Seed) Close() {
	if s.srv != nil {
		s.srv.Shutdown(context.Background())
	}
}

// handleBootstrap 处理冷启动握手：登记新节点并回传全量成员快照。
func (s *Seed) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req bootstrapRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Meta.Heartbeat == 0 {
		req.Meta.Heartbeat = time.Now().UnixNano()
	}
	s.store.Upsert(req.Meta)
	writeJSON(w, bootstrapResponse{Members: s.store.Snapshot()})
}

// handleGossip 与常规节点做双向成员合并。
func (s *Seed) handleGossip(w http.ResponseWriter, r *http.Request) {
	var req gossipRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.store.Merge(req.Members)
	writeJSON(w, gossipResponse{Members: s.store.Snapshot()})
}
