// Package leasecache 实现一个基于租约（Lease）的分布式缓存失效一致性保证器，
// 语义类似 AFS / Leases 协议：
//
//   - 客户端向服务端请求某文件的读租约，租约期内可绝对放心地读本地缓存；
//   - 写客户端修改文件时，服务端先回调通知所有租约持有者撤销租约；
//   - 若持有者失联，服务端安全阻塞至租约过期后才放行写入，保证零脏读。
package leasecache

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
)

// DefaultLeaseTTL 是默认的读租约时长。
const DefaultLeaseTTL = 5 * time.Second

// LeaseRequest 是客户端申请读租约的请求。
type LeaseRequest struct {
	Path     string `json:"path"`
	ClientID string `json:"client_id"`
	Callback string `json:"callback"` // 客户端撤销回调地址，如 http://client-1
}

// LeaseResponse 是服务端授予租约的响应，携带文件内容快照。
type LeaseResponse struct {
	LeaseID   uint64 `json:"lease_id"`
	Path      string `json:"path"`
	Content   []byte `json:"content"`
	Version   uint64 `json:"version"`
	ExpiresAt int64  `json:"expires_at"` // UnixNano
}

// WriteRequest 是写客户端的修改请求。该请求会阻塞到可以安全写入为止。
type WriteRequest struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// WriteResponse 是写入完成后的响应。
type WriteResponse struct {
	Path    string `json:"path"`
	Version uint64 `json:"version"`
}

// RevokeRequest 是服务端发往客户端回调地址的租约撤销通知。
type RevokeRequest struct {
	LeaseID uint64 `json:"lease_id"`
	Path    string `json:"path"`
}

type lease struct {
	id        uint64
	clientID  string
	callback  string
	expiresAt time.Time
}

type fileState struct {
	content []byte
	version uint64
	leases  map[uint64]*lease
}

// Server 是租约服务端，通过 HTTP/JSON 对外提供 /lease 与 /write 接口。
type Server struct {
	ttl time.Duration

	// Transport 可选：自定义拨号（如 PipeNetwork.HTTPTransport()）。
	// 为 nil 时使用 http.DefaultTransport（TCP）。
	Transport http.RoundTripper

	// Logf 可选，设置后服务端会输出关键事件日志（演示用）。
	Logf func(format string, args ...interface{})

	mu      sync.Mutex
	files   map[string]*fileState
	nextID  uint64
	changed chan struct{} // 租约集合变化（撤销成功）时收到信号

	httpServer *http.Server
	listener   net.Listener
	revokeCli  *http.Client
}

// NewServer 创建一个租约服务端，ttl 为读租约时长。
func NewServer(ttl time.Duration) *Server {
	return &Server{
		ttl:     ttl,
		files:   make(map[string]*fileState),
		changed: make(chan struct{}, 1),
	}
}

// Start 通过 TCP 在 127.0.0.1 的随机端口上启动服务（真实部署用）。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	return s.StartOn(ln)
}

// StartOn 在指定 listener 上启动服务（可传 PipeNetwork 的 listener）。
func (s *Server) StartOn(ln net.Listener) error {
	s.listener = ln
	tr := s.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	s.revokeCli = &http.Client{Transport: tr, Timeout: time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/lease", s.handleLease)
	mux.HandleFunc("/write", s.handleWrite)
	s.httpServer = &http.Server{Handler: mux}
	go s.httpServer.Serve(ln)
	return nil
}

// Close 关闭服务端。
func (s *Server) Close() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

// URL 返回服务端的 base URL。
func (s *Server) URL() string {
	return "http://" + s.listener.Addr().String()
}

// SetFile 初始化（播种）一个文件的内容，不走租约协议。
func (s *Server) SetFile(path string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fileLocked(path)
	f.content = append([]byte(nil), content...)
	f.version++
}

// Version 返回文件当前版本号（测试/演示辅助）。
func (s *Server) Version(path string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileLocked(path).version
}

func (s *Server) fileLocked(path string) *fileState {
	f, ok := s.files[path]
	if !ok {
		f = &fileState{leases: make(map[uint64]*lease)}
		s.files[path] = f
	}
	return f
}

func (s *Server) logf(format string, args ...interface{}) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	var req LeaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	f := s.fileLocked(req.Path)
	s.nextID++
	id := s.nextID
	exp := time.Now().Add(s.ttl)
	f.leases[id] = &lease{id: id, clientID: req.ClientID, callback: req.Callback, expiresAt: exp}
	resp := LeaseResponse{
		LeaseID:   id,
		Path:      req.Path,
		Content:   append([]byte(nil), f.content...),
		Version:   f.version,
		ExpiresAt: exp.UnixNano(),
	}
	s.mu.Unlock()
	s.logf("授予读租约: client=%s path=%s lease=%d 过期于 %v 后", req.ClientID, req.Path, id, s.ttl)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request) {
	var req WriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version := s.write(req.Path, req.Content)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(WriteResponse{Path: req.Path, Version: version})
}

// write 阻塞直到不存在任何未过期的读租约，然后应用修改。
// 这是“零脏读”的核心：只要还有有效租约，写入绝不会生效。
func (s *Server) write(path string, content []byte) uint64 {
	notified := make(map[uint64]bool)
	for {
		s.mu.Lock()
		f := s.fileLocked(path)
		now := time.Now()
		var wait time.Duration
		for id, l := range f.leases {
			if !now.Before(l.expiresAt) {
				delete(f.leases, id) // 租约已过期，直接清除
				continue
			}
			if d := l.expiresAt.Sub(now); wait == 0 || d < wait {
				wait = d
			}
		}
		if len(f.leases) == 0 {
			f.content = append([]byte(nil), content...)
			f.version++
			v := f.version
			s.mu.Unlock()
			s.logf("写入生效: path=%s version=%d", path, v)
			return v
		}
		var pending []*lease
		for id, l := range f.leases {
			if !notified[id] {
				notified[id] = true
				pending = append(pending, l)
			}
		}
		s.mu.Unlock()

		for _, l := range pending {
			s.logf("写入被阻塞: path=%s 存在有效租约 lease=%d (client=%s)，通知撤销", path, l.id, l.clientID)
			go s.revoke(path, l)
		}

		timer := time.NewTimer(wait)
		select {
		case <-s.changed: // 有租约被成功撤销，重新检查
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C: // 最近的租约已到期，重新检查
		}
	}
}

// revoke 回调客户端撤销租约；成功则从租约表中移除并唤醒写流程。
// 客户端失联（连接拒绝/超时）时什么都不做，由写流程阻塞至租约过期。
func (s *Server) revoke(path string, l *lease) {
	body, _ := json.Marshal(RevokeRequest{LeaseID: l.id, Path: path})
	resp, err := s.revokeCli.Post(l.callback+"/revoke", "application/json", bytes.NewReader(body))
	if err != nil {
		s.logf("撤销通知失败（client=%s 疑似失联）: lease=%d，将安全阻塞至租约过期", l.clientID, l.id)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	s.mu.Lock()
	if f, ok := s.files[path]; ok {
		delete(f.leases, l.id)
	}
	s.mu.Unlock()
	s.logf("租约已撤销: client=%s lease=%d", l.clientID, l.id)
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
