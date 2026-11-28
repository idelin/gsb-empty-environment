package jar

import (
	"crypto"
	"sync"
)

// Client 表示一个已注册 OAuth 客户端的安全元数据。
// 解析器只关心强制签名策略与验签公钥。
type Client interface {
	// ID 返回 client_id。
	ID() string
	// RequireSignedRequestObject 返回该客户端是否强制 JAR（RFC 9101 §5.2）。
	RequireSignedRequestObject() bool
	// KeyByKID 按 JWT 头部 kid 查找验签公钥。
	// kid 为空且客户端只注册了一把默认公钥时允许返回该默认公钥。
	KeyByKID(kid string) (crypto.PublicKey, bool)
}

// ClientStore 是解析器查找客户端的抽象，宿主可自行实现（例如接数据库）。
type ClientStore interface {
	Lookup(clientID string) (Client, bool)
}

// RegisteredClient 是 Client 的内存实现，配合 Registry 使用。
type RegisteredClient struct {
	id         string
	requireJAR bool
	mu         sync.RWMutex
	keys       map[string]crypto.PublicKey
	defaultKID string
}

// NewRegisteredClient 创建一个客户端。defaultKID 为空时取首个加入的 kid。
func NewRegisteredClient(id string, requireJAR bool) *RegisteredClient {
	return &RegisteredClient{
		id:         id,
		requireJAR: requireJAR,
		keys:       make(map[string]crypto.PublicKey),
	}
}

func (c *RegisteredClient) ID() string { return c.id }

func (c *RegisteredClient) RequireSignedRequestObject() bool { return c.requireJAR }

// AddKey 注册一把按 kid 索引的非对称公钥。
func (c *RegisteredClient) AddKey(kid string, key crypto.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[kid] = key
	if c.defaultKID == "" {
		c.defaultKID = kid
	}
}

// KeyByKID 实现 Client。空 kid 且仅有一把密钥时允许默认解析，
// 多密钥客户端必须在 JWT 头部显式携带 kid。
func (c *RegisteredClient) KeyByKID(kid string) (crypto.PublicKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if kid != "" {
		key, ok := c.keys[kid]
		return key, ok
	}
	if len(c.keys) == 1 {
		return c.keys[c.defaultKID], true
	}
	return nil, false
}

// Registry 是线程安全的内存 ClientStore。
type Registry struct {
	mu      sync.RWMutex
	clients map[string]*RegisteredClient
}

// NewRegistry 创建空客户端注册表。
func NewRegistry() *Registry {
	return &Registry{clients: make(map[string]*RegisteredClient)}
}

// Register 加入或替换一个客户端。
func (r *Registry) Register(client *RegisteredClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[client.id] = client
}

// Lookup 实现 ClientStore。
func (r *Registry) Lookup(clientID string) (Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[clientID]
	if !ok {
		return nil, false
	}
	return client, true
}
