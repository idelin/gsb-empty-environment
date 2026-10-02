package leasecache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

type cacheEntry struct {
	content   []byte
	version   uint64
	leaseID   uint64
	expiresAt time.Time
}

// Client 是缓存客户端：持有本地缓存，并通过回调端口接收服务端的租约撤销通知。
type Client struct {
	id        string
	serverURL string

	// Transport 可选：自定义拨号（如 PipeNetwork.HTTPTransport()）。
	// 为 nil 时使用 http.DefaultTransport（TCP）。
	Transport http.RoundTripper

	mu     sync.Mutex
	cache  map[string]*cacheEntry
	client *http.Client

	callbackURL string
	httpServer  *http.Server
	listener    net.Listener
}

// NewClient 创建一个客户端。serverURL 为服务端 base URL。
func NewClient(id, serverURL string) *Client {
	return &Client{
		id:        id,
		serverURL: serverURL,
		cache:     make(map[string]*cacheEntry),
	}
}

// Start 通过 TCP 在 127.0.0.1 的随机端口启动撤销回调监听（真实部署用）。
func (c *Client) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	return c.StartOn(ln)
}

// StartOn 在指定 listener 上启动撤销回调监听（可传 PipeNetwork 的 listener）。
func (c *Client) StartOn(ln net.Listener) error {
	c.listener = ln
	c.callbackURL = "http://" + ln.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", c.handleRevoke)
	c.httpServer = &http.Server{Handler: mux}
	go c.httpServer.Serve(ln)
	return nil
}

// Close 关闭客户端。
func (c *Client) Close() error {
	if c.httpServer != nil {
		return c.httpServer.Close()
	}
	return nil
}

// Disconnect 模拟客户端失联（崩溃/网络分区）：关闭回调端口，
// 但本地缓存仍然保留（租约期内继续读缓存是安全的，因为服务端会阻塞写入）。
func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.httpServer != nil {
		c.httpServer.Close()
		c.httpServer = nil
	}
}

// Read 读取文件。租约有效期内直接返回本地缓存（绝不脏读）；
// 否则向服务端申请新租约并刷新缓存。
func (c *Client) Read(path string) ([]byte, uint64, error) {
	c.mu.Lock()
	if e, ok := c.cache[path]; ok && time.Now().Before(e.expiresAt) {
		content := append([]byte(nil), e.content...)
		version := e.version
		c.mu.Unlock()
		return content, version, nil
	}
	c.mu.Unlock()

	reqBody, _ := json.Marshal(LeaseRequest{Path: path, ClientID: c.id, Callback: c.callbackURL})
	resp, err := c.http().Post(c.serverURL+"/lease", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("lease request failed: %s", resp.Status)
	}
	var lr LeaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, 0, err
	}
	c.mu.Lock()
	c.cache[path] = &cacheEntry{
		content:   append([]byte(nil), lr.Content...),
		version:   lr.Version,
		leaseID:   lr.LeaseID,
		expiresAt: time.Unix(0, lr.ExpiresAt),
	}
	c.mu.Unlock()
	return lr.Content, lr.Version, nil
}

// Write 请求修改文件。调用会阻塞，直到服务端确认所有读租约已撤销或过期。
func (c *Client) Write(path string, content []byte) (uint64, error) {
	reqBody, _ := json.Marshal(WriteRequest{Path: path, Content: content})
	resp, err := c.http().Post(c.serverURL+"/write", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("write failed: %s", resp.Status)
	}
	var wr WriteResponse
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		return 0, err
	}
	return wr.Version, nil
}

// Cached 返回指定路径当前是否有未过期的本地缓存（测试辅助）。
func (c *Client) Cached(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[path]
	return ok && time.Now().Before(e.expiresAt)
}

func (c *Client) http() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		tr := c.Transport
		if tr == nil {
			tr = http.DefaultTransport
		}
		c.client = &http.Client{Transport: tr}
	}
	return c.client
}

// handleRevoke 处理服务端的撤销通知：使本地缓存失效并确认。
func (c *Client) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var req RevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if e, ok := c.cache[req.Path]; ok && e.leaseID == req.LeaseID {
		delete(c.cache, req.Path)
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}
