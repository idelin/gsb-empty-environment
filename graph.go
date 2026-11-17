package netsec

import (
	"errors"
	"sync"
)

// ObjectID 标识一份被缓存宿主登记的敏感对象（明文来源、缓存副本或派生片段）。
type ObjectID string

// KeyGen 是密钥代编号。同一来源对象在不同密钥代下的副本互相独立，
// 按代撤销只会影响绑定到该密钥代的对象。
type KeyGen uint64

var (
	// ErrObjectExists 表示登记时对象 ID 已被占用。
	ErrObjectExists = errors.New("netsec: sensitive object already registered")
	// ErrObjectNotFound 表示引用的对象 ID 不存在，或撤销的密钥代下没有任何对象。
	ErrObjectNotFound = errors.New("netsec: sensitive object not found")
	// ErrRevoked 表示对象（或其任一来源）已被撤销，登记或取用被拒绝。
	ErrRevoked = errors.New("netsec: sensitive object has been revoked")
	// ErrInvalidSources 表示派生物没有提供任何来源，或数据切片非法。
	ErrInvalidSources = errors.New("netsec: invalid registration arguments")
)

// Wiper 在对象可以安全擦除时被调用，负责清零 data 指向的底层存储。
// 默认实现把 data 的全部字节写零。自定义实现不得回调同一个 Graph，
// 也不应阻塞，因为调用发生在 Graph 的内部临界区内。
type Wiper func(id ObjectID, gen KeyGen, data []byte)

// DefaultWiper 清零整块底层数组；nil 数据切片按空处理。
func DefaultWiper(_ ObjectID, _ KeyGen, data []byte) {
	clear(data)
}

// Graph 维护敏感对象之间的来源依赖关系及其生命周期。
//
// 缓存宿主在把明文放入读缓存、索引缓存或派生片段时登记对象并绑定来源与
// 密钥代；撤销某个对象时，Graph 沿“依赖者”边求出依赖闭包，把闭包内全部
// 对象标记为不可用，等待所有在用句柄释放后再逐一擦除底层字节。
// 含多个来源的共享派生物只要任一来源被撤销就会进入闭包而失效。
type Graph struct {
	mu       sync.Mutex
	cond     *sync.Cond
	objects  map[ObjectID]*object
	wipeHook Wiper
}

type object struct {
	id         ObjectID
	gen        KeyGen
	data       []byte
	sources    []ObjectID
	dependents []ObjectID
	revoked    bool
	wiped      bool
	holders    int
}

// Option 调整 Graph 的行为，主要用于测试注入擦除观测点。
type Option func(*Graph)

// WithWiper 替换默认的清零逻辑。wiper 为 nil 时回退到 DefaultWiper。
func WithWiper(wiper Wiper) Option {
	return func(g *Graph) {
		if wiper != nil {
			g.wipeHook = wiper
		}
	}
}

// NewGraph 创建空的敏感对象依赖图。
func NewGraph(opts ...Option) *Graph {
	g := &Graph{
		objects:  make(map[ObjectID]*object),
		wipeHook: DefaultWiper,
	}
	g.cond = sync.NewCond(&g.mu)
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// RegisterSource 登记一份没有上游来源的明文根对象（例如刚解密出的明文）。
// ID 重复时返回 ErrObjectExists。
func (g *Graph) RegisterSource(id ObjectID, gen KeyGen, data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[id]; ok {
		return ErrObjectExists
	}
	g.objects[id] = &object{id: id, gen: gen, data: data}
	return nil
}

// RegisterCopy 登记来源对象的一份同代副本，例如读缓存或索引缓存中的拷贝。
// 来源未知返回 ErrObjectNotFound，来源已撤销返回 ErrRevoked：
// 撤销之后不允许再产生新的副本，防止被擦除的数据借新登记回流。
func (g *Graph) RegisterCopy(id ObjectID, source ObjectID, data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	src, ok := g.objects[source]
	if !ok {
		return ErrObjectNotFound
	}
	if src.revoked {
		return ErrRevoked
	}
	return g.registerLocked(id, src.gen, data, []ObjectID{source})
}

// RegisterDerived 登记一个共享派生片段，其内容由 sources 中的一个或多个
// 来源对象派生而来（例如多份明文合并出的索引片段）。任一来源未知、已撤销
// 都会拒绝登记；重复的来源 ID 自动去重。
func (g *Graph) RegisterDerived(id ObjectID, gen KeyGen, data []byte, sources []ObjectID) error {
	if len(sources) == 0 {
		return ErrInvalidSources
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	uniq := make([]ObjectID, 0, len(sources))
	seen := make(map[ObjectID]struct{}, len(sources))
	for _, sid := range sources {
		src, ok := g.objects[sid]
		if !ok {
			return ErrObjectNotFound
		}
		if src.revoked {
			return ErrRevoked
		}
		if _, dup := seen[sid]; dup {
			continue
		}
		seen[sid] = struct{}{}
		uniq = append(uniq, sid)
	}
	return g.registerLocked(id, gen, data, uniq)
}

func (g *Graph) registerLocked(id ObjectID, gen KeyGen, data []byte, sources []ObjectID) error {
	if _, ok := g.objects[id]; ok {
		return ErrObjectExists
	}
	o := &object{id: id, gen: gen, data: data, sources: append([]ObjectID(nil), sources...)}
	g.objects[id] = o
	for _, sid := range sources {
		src := g.objects[sid]
		src.dependents = append(src.dependents, id)
	}
	return nil
}

// Acquire 取用对象，返回的句柄会阻止对象在使用期间被擦除。
// 对象未知返回 ErrObjectNotFound，已撤销（含传递撤销）返回 ErrRevoked。
// 调用方必须在使用结束后调用 Handle.Release，建议配合 defer 使用。
func (g *Graph) Acquire(id ObjectID) (*Handle, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	if !ok {
		return nil, ErrObjectNotFound
	}
	if o.revoked {
		return nil, ErrRevoked
	}
	o.holders++
	return &Handle{g: g, o: o}, nil
}

// Handle 是一次在用登记。句柄存活期间底层字节保证不会被清零。
type Handle struct {
	g        *Graph
	o        *object
	released atomicBool
}

// Bytes 返回敏感对象的底层字节。切片与登记时共享底层数组，
// 调用方不得在 Release 之后继续访问。
func (h *Handle) Bytes() []byte { return h.o.data }

// Len 返回底层字节长度。
func (h *Handle) Len() int { return len(h.o.data) }

// ID 返回对象 ID。
func (h *Handle) ID() ObjectID { return h.o.id }

// Gen 返回对象绑定的密钥代。
func (h *Handle) Gen() KeyGen { return h.o.gen }

// Released 报告句柄是否已经归还。
func (h *Handle) Released() bool { return h.released.Load() }

// Release 归还句柄。当最后一个使用者退出时，等待中的撤销会继续擦除。
// 重复调用是空操作，保证 defer 与显式释放可以安全并存。
func (h *Handle) Release() {
	if !h.released.CompareAndSwap(false, true) {
		return
	}
	g := h.g
	g.mu.Lock()
	h.o.holders--
	if h.o.holders == 0 {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
}

// Revoke 撤销 id 及其整个依赖闭包：闭包内对象立即标记为不可用，
// 新的登记与取用都会被拒绝；已发放的句柄仍然有效，等全部释放后，
// 各对象的底层字节才会被擦除。调用返回时闭包内对象均已擦除完成。
// 撤销未知 ID 返回 ErrObjectNotFound；重复撤销是幂等的空操作语义。
func (g *Graph) Revoke(id ObjectID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, ok := g.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	closure := g.dependentClosureLocked(root)
	for _, o := range closure {
		o.revoked = true
	}
	for _, o := range closure {
		g.waitAndWipeLocked(o)
	}
	return nil
}

// RevokeGeneration 撤销绑定到 gen 的所有对象及其依赖闭包。
// 该密钥代下没有任何对象时返回 ErrObjectNotFound。
func (g *Graph) RevokeGeneration(gen KeyGen) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var roots []*object
	for _, o := range g.objects {
		if o.gen == gen {
			roots = append(roots, o)
		}
	}
	if len(roots) == 0 {
		return ErrObjectNotFound
	}
	closureSet := make(map[ObjectID]*object)
	for _, root := range roots {
		for _, o := range g.dependentClosureLocked(root) {
			closureSet[o.id] = o
		}
	}
	for _, o := range closureSet {
		o.revoked = true
	}
	for _, o := range closureSet {
		g.waitAndWipeLocked(o)
	}
	return nil
}

// dependentClosureLocked 以 root 为起点沿 dependents 边做 BFS，
// 返回 root 自身以及全部（含传递的）依赖者。
func (g *Graph) dependentClosureLocked(root *object) []*object {
	seen := map[ObjectID]*object{root.id: root}
	queue := []*object{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, depID := range cur.dependents {
			if _, ok := seen[depID]; ok {
				continue
			}
			dep := g.objects[depID]
			seen[depID] = dep
			queue = append(queue, dep)
		}
	}
	out := make([]*object, 0, len(seen))
	for _, o := range seen {
		out = append(out, o)
	}
	return out
}

// waitAndWipeLocked 阻塞到对象没有在用句柄，然后擦除一次。
// 调用时必须持有 g.mu；cond.Wait 会临时释放锁。
func (g *Graph) waitAndWipeLocked(o *object) {
	for o.holders > 0 {
		g.cond.Wait()
	}
	if o.wiped {
		return
	}
	g.wipeHook(o.id, o.gen, o.data)
	o.wiped = true
}

// IsRevoked 报告对象是否已被撤销（含被依赖闭包波及）。未知 ID 返回 false。
func (g *Graph) IsRevoked(id ObjectID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	return ok && o.revoked
}

// IsWiped 报告对象的底层字节是否已经擦除。未知 ID 返回 false。
func (g *Graph) IsWiped(id ObjectID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	return ok && o.wiped
}

// Holders 返回对象当前未释放的在用句柄数，主要用于测试与可观测性。
func (g *Graph) Holders(id ObjectID) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	if !ok {
		return 0, ErrObjectNotFound
	}
	return o.holders, nil
}

// Len 返回依赖图当前登记的对象总数（含已擦除的对象）。
func (g *Graph) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.objects)
}

// Wiped 返回已经完成擦除的对象 ID 快照，顺序未指定。
func (g *Graph) Wiped() []ObjectID {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ObjectID
	for id, o := range g.objects {
		if o.wiped {
			out = append(out, id)
		}
	}
	return out
}
