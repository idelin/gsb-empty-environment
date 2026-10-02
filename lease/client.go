package lease

import (
	"fmt"
	"net"
	"net/rpc"
	"sync"
	"time"
)

type cacheEntry struct {
	content   []byte
	version   uint64
	leaseID   string
	expiresAt time.Time
}

// Client 是缓存客户端：持有读租约期间直接读本地缓存，
// 并通过回调地址接收服务端的租约撤销通知。
type Client struct {
	name       string
	serverAddr string
	transport  Transport

	mu     sync.Mutex
	cache  map[string]*cacheEntry
	closed bool

	ln        net.Listener
	rpcServer *rpc.Server
}

// NewClient 创建客户端并启动回调监听（用于接收撤销通知）。
func NewClient(name, serverAddr string, transport Transport) (*Client, error) {
	ln, err := transport.Listen()
	if err != nil {
		return nil, err
	}
	c := &Client{
		name:       name,
		serverAddr: serverAddr,
		transport:  transport,
		cache:      make(map[string]*cacheEntry),
		ln:         ln,
	}
	c.rpcServer = rpc.NewServer()
	if err := c.rpcServer.RegisterName("Callback", &callbackHandler{client: c}); err != nil {
		ln.Close()
		return nil, err
	}
	go serve(c.rpcServer, ln)
	return c, nil
}

// Addr 返回客户端的回调地址。
func (c *Client) Addr() string { return c.ln.Addr().String() }

// Disconnect 模拟客户端失联：关闭回调监听，服务端的撤销通知将无法送达。
func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.ln.Close()
	}
}

// Close 释放客户端资源。
func (c *Client) Close() { c.Disconnect() }

// Read 读取文件。租约有效期内直接命中本地缓存（不发起 RPC）；
// 否则向服务端申请新的读租约并刷新缓存。
// 返回值 hit 表示是否命中本地缓存。
func (c *Client) Read(path string) (content []byte, hit bool, err error) {
	c.mu.Lock()
	if e, ok := c.cache[path]; ok && time.Now().Before(e.expiresAt) {
		out := append([]byte(nil), e.content...)
		c.mu.Unlock()
		return out, true, nil
	}
	c.mu.Unlock()

	var reply ReadReply
	if err := c.call("LeaseServer.ReadFile", &ReadArgs{
		Path:         path,
		CallbackAddr: c.Addr(),
	}, &reply); err != nil {
		return nil, false, err
	}

	c.mu.Lock()
	c.cache[path] = &cacheEntry{
		content:   append([]byte(nil), reply.Content...),
		version:   reply.Version,
		leaseID:   reply.LeaseID,
		expiresAt: reply.ExpiresAt,
	}
	c.mu.Unlock()
	return append([]byte(nil), reply.Content...), false, nil
}

// Write 请求服务端修改文件。若存在活跃读租约，此调用会阻塞，
// 直到所有租约被撤销或过期后才生效。
func (c *Client) Write(path string, content []byte) (uint64, error) {
	var reply WriteReply
	err := c.call("LeaseServer.WriteFile", &WriteArgs{Path: path, Content: content}, &reply)
	return reply.Version, err
}

func (c *Client) call(method string, args, reply interface{}) error {
	conn, err := c.transport.Dial(c.serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	rc := rpc.NewClient(conn)
	defer rc.Close()
	return rc.Call(method, args, reply)
}

// callbackHandler 处理服务端的租约撤销回调。
type callbackHandler struct {
	client *Client
}

// RevokeLease 是 RPC 回调方法：失效本地缓存并确认。
func (h *callbackHandler) RevokeLease(args *RevokeArgs, reply *RevokeReply) error {
	c := h.client
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, e := range c.cache {
		if e.leaseID == args.LeaseID {
			delete(c.cache, path)
			return nil
		}
	}
	return fmt.Errorf("unknown lease %s", args.LeaseID)
}
