package lease

import (
	"fmt"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"
	"time"
)

// Lease 表示一个已授予的读租约。
type Lease struct {
	ID           string
	CallbackAddr string
	ExpiresAt    time.Time
}

type fileState struct {
	mu      sync.Mutex
	cond    *sync.Cond
	content []byte
	version uint64
	leases  map[string]*Lease
	writing bool // 有写者正在等待租约失效；期间暂停发放新租约
}

// Server 是租约服务端，类似 AFS/Leases 协议中的服务器角色。
//
// 一致性保证：对某个文件的写入，在该文件所有已授予租约
// 被确认撤销或自然过期之前，绝不会生效。因此持有有效租约的
// 客户端读本地缓存永远不会读到脏数据（零读脏）。
type Server struct {
	leaseDuration time.Duration
	transport     Transport
	callTimeout   time.Duration

	mu        sync.Mutex
	files     map[string]*fileState
	idCounter uint64
	reads     uint64 // 实际处理的 ReadFile RPC 次数（用于验证缓存命中）
}

// NewServer 创建服务端，leaseDuration 为每个读租约的有效期，
// transport 用于向客户端发送撤销回调。
func NewServer(leaseDuration time.Duration, transport Transport) *Server {
	return &Server{
		leaseDuration: leaseDuration,
		transport:     transport,
		callTimeout:   2 * time.Second,
		files:         make(map[string]*fileState),
	}
}

func (s *Server) file(path string) *fileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[path]
	if !ok {
		f = &fileState{leases: make(map[string]*Lease)}
		f.cond = sync.NewCond(&f.mu)
		s.files[path] = f
	}
	return f
}

// ReadCount 返回服务端实际处理过的读请求数（缓存未命中才会触发）。
func (s *Server) ReadCount() uint64 { return atomic.LoadUint64(&s.reads) }

// ReadFile 是 RPC 方法：授予客户端一个读租约并返回当前内容。
// 若有写者正在等待租约失效，新的租约请求会被阻塞，直到写入完成，
// 以保证写者的等待时间有界（不会超过一个租约周期）。
func (s *Server) ReadFile(args *ReadArgs, reply *ReadReply) error {
	atomic.AddUint64(&s.reads, 1)
	f := s.file(args.Path)

	f.mu.Lock()
	for f.writing {
		f.cond.Wait()
	}
	id := atomic.AddUint64(&s.idCounter, 1)
	leaseID := fmt.Sprintf("lease-%d", id)
	expiresAt := time.Now().Add(s.leaseDuration)
	f.leases[leaseID] = &Lease{
		ID:           leaseID,
		CallbackAddr: args.CallbackAddr,
		ExpiresAt:    expiresAt,
	}
	reply.Content = append([]byte(nil), f.content...)
	reply.Version = f.version
	reply.LeaseID = leaseID
	reply.ExpiresAt = expiresAt
	f.mu.Unlock()
	return nil
}

// WriteFile 是 RPC 方法：修改文件。
// 关键语义：先撤销该文件所有活跃读租约（回调客户端），
// 对失联客户端安全阻塞直至其租约自然过期，然后才应用写入。
func (s *Server) WriteFile(args *WriteArgs, reply *WriteReply) error {
	f := s.file(args.Path)

	f.mu.Lock()
	f.writing = true
	leases := make([]*Lease, 0, len(f.leases))
	for _, l := range f.leases {
		leases = append(leases, l)
	}
	f.mu.Unlock()

	s.revokeAll(f, leases)

	f.mu.Lock()
	f.content = append([]byte(nil), args.Content...)
	f.version++
	reply.Version = f.version
	f.writing = false
	f.cond.Broadcast()
	f.mu.Unlock()
	return nil
}

// revokeAll 并发回调所有租约持有者；全部确认则立即返回，
// 否则阻塞至最晚的租约过期时刻，保证零读脏。
func (s *Server) revokeAll(f *fileState, leases []*Lease) {
	if len(leases) == 0 {
		return
	}
	ack := make(chan string, len(leases))
	var deadline time.Time
	for _, l := range leases {
		if l.ExpiresAt.After(deadline) {
			deadline = l.ExpiresAt
		}
		go func(l *Lease) {
			if s.callbackRevoke(l) {
				ack <- l.ID
			}
		}(l)
	}

	remaining := make(map[string]bool, len(leases))
	for _, l := range leases {
		remaining[l.ID] = true
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for len(remaining) > 0 {
		select {
		case id := <-ack:
			delete(remaining, id)
			f.mu.Lock()
			delete(f.leases, id)
			f.mu.Unlock()
		case <-timer.C:
			// 到达最晚过期时刻：所有未应答租约均已自然过期，
			// 持有者再读本地缓存已超出租约期，写入安全。
			f.mu.Lock()
			for id := range remaining {
				delete(f.leases, id)
			}
			f.mu.Unlock()
			return
		}
	}
}

// callbackRevoke 通过 RPC 回调客户端撤销租约；客户端失联时返回 false。
func (s *Server) callbackRevoke(l *Lease) bool {
	conn, err := s.transport.Dial(l.CallbackAddr)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.callTimeout))
	client := rpc.NewClient(conn)
	defer client.Close()
	var reply RevokeReply
	if err := client.Call("Callback.RevokeLease", &RevokeArgs{LeaseID: l.ID}, &reply); err != nil {
		return false
	}
	return true
}

// Serve 在监听地址上提供 RPC 服务（在后台 goroutine 中接受连接）。
func (s *Server) Serve(ln net.Listener) {
	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterName("LeaseServer", s); err != nil {
		panic(err)
	}
	go serve(rpcServer, ln)
}

// serve 循环接受连接；监听关闭时安静退出。
func serve(rs *rpc.Server, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go rs.ServeConn(conn)
	}
}
