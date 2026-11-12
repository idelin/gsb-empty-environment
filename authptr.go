package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// 认证标签的域分隔前缀，避免与其它使用同一 MAC 密钥的消息混用。
var tagDomainPrefix = []byte("netsec-authptr-v1\x00")

// 常见校验失败原因。调用方应使用 errors.Is 判定，而非比较错误文本。
var (
	// ErrNilMAC 表示构造注册表时没有注入 MAC 实现。
	ErrNilMAC = errors.New("netsec: nil MAC implementation")
	// ErrInvalidPointer 表示指针字段本身不合法（如零地址）。
	ErrInvalidPointer = errors.New("netsec: invalid authenticated pointer")
	// ErrInvalidKind 表示指针种类不在已知枚举内。
	ErrInvalidKind = errors.New("netsec: invalid pointer kind")
	// ErrInvalidContext 表示调用上下文缺少域标识。
	ErrInvalidContext = errors.New("netsec: invalid call context")
	// ErrTagMismatch 表示重算标签与携带标签不一致，字段被篡改或来自其它对象。
	ErrTagMismatch = errors.New("netsec: pointer authentication tag mismatch")
	// ErrUnknownObject 表示地址未在当前注册表登记。
	ErrUnknownObject = errors.New("netsec: unknown object address")
	// ErrStaleGeneration 表示地址已被新对象重用，标签属于已废弃的旧代。
	ErrStaleGeneration = errors.New("netsec: stale pointer generation, address was reused")
	// ErrRetired 表示对象在验证通过后已被退役，使用门禁拒绝放行。
	ErrRetired = errors.New("netsec: object generation retired")
	// ErrContextMismatch 表示指针标签绑定的调用上下文与当前调用不一致。
	ErrContextMismatch = errors.New("netsec: call context does not match pointer binding")
	// ErrWidenedKind 表示派生指针试图把种类提升到比父指针更宽的权限。
	ErrWidenedKind = errors.New("netsec: derived pointer kind widens parent kind")
)

// Kind 标识指针的静态种类。不同种类对应不同的使用门禁，
// 数据指针永远不能通过函数指针的调用门禁，反之亦然。
type Kind uint8

const (
	// KindUnknown 是零值，仅用于拒绝未初始化的种类。
	KindUnknown Kind = 0
	// KindData 是数据指针，只允许解引用门禁。
	KindData Kind = 1
	// KindFunction 是函数指针，只允许调用门禁，权限等级高于数据指针。
	KindFunction Kind = 2
)

// rank 给出种类的权限等级，派生时新种类的等级不得高于父种类，
// 即只允许保留同等级或向更低等级收窄（如函数指针收窄为数据指针）。
func (k Kind) rank() (uint8, error) {
	switch k {
	case KindData:
		return 1, nil
	case KindFunction:
		return 2, nil
	default:
		return 0, fmt.Errorf("%w: %d", ErrInvalidKind, k)
	}
}

// String 便于日志与错误信息输出。
func (k Kind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindFunction:
		return "function"
	default:
		return "unknown"
	}
}

// Context 是指针标签绑定的调用上下文：安全域加具体调用点。
// 同一对象在不同上下文下使用会得到不同标签，门禁据此阻止跨上下文交换。
type Context struct {
	// Domain 是安全域/模块标识，不允许为空。
	Domain string
	// CallSite 是调用点标识（如运行时内部的调用点编号）。
	CallSite uint64
}

func (c Context) valid() error {
	if c.Domain == "" {
		return fmt.Errorf("%w: empty domain", ErrInvalidContext)
	}
	if len(c.Domain) > 0xffff {
		return fmt.Errorf("%w: domain exceeds %d bytes", ErrInvalidContext, 0xffff)
	}
	return nil
}

// MAC 是注入式消息认证码接口。运行时可注入任意具备定长标签与
// 常量时间比较能力的 MAC 实现（生产中通常是 HMAC 或硬件 PAC 包装器）。
type MAC interface {
	// Tag 对确定性编码后的指针材料计算认证标签。
	Tag(input []byte) []byte
	// Equal 以常量时间方式比较两个标签，避免计时侧信道。
	Equal(expected, actual []byte) bool
}

// AuthenticatedPointer 是携带认证标签的指针封装。
// 字段可导出以便运行时序列化，但任何篡改都会在 Verify 处被标签拒绝；
// 唯一合法的构造途径是 Registry 的 Mint/Derive。
type AuthenticatedPointer struct {
	address    uintptr
	generation uint64
	kind       Kind
	context    Context
	tag        []byte
}

// Address 返回被封装的原始地址。
func (p AuthenticatedPointer) Address() uintptr { return p.address }

// Generation 返回标签绑定的对象代次。
func (p AuthenticatedPointer) Generation() uint64 { return p.generation }

// PointerKind 返回标签绑定的指针种类。
func (p AuthenticatedPointer) PointerKind() Kind { return p.kind }

// UseContext 返回标签绑定的调用上下文。
func (p AuthenticatedPointer) UseContext() Context { return p.context }

// Tag 返回认证标签的副本，防止调用方就地修改注册表内部状态。
func (p AuthenticatedPointer) Tag() []byte {
	return append([]byte(nil), p.tag...)
}

// Registry 维护对象地址到代次的映射与退役代次集合，
// 并通过注入的 MAC 为指针签发和校验认证标签。
// 所有方法均为并发安全。
type Registry struct {
	mu  sync.Mutex
	mac MAC

	// nextGen 是单调递增的代次计数器，每次 Mint 都分配新代次。
	nextGen uint64
	// objects 记录每个当前登记地址所属的代次；地址重用即覆盖旧映射。
	objects map[uintptr]uint64
	// retired 记录已退役的代次，派生的内部指针共享同一代次，退役一并失效。
	retired map[uint64]bool
}

// NewRegistry 用注入的 MAC 实现构造指针认证注册表。
func NewRegistry(mac MAC) (*Registry, error) {
	if mac == nil {
		return nil, ErrNilMAC
	}
	return &Registry{
		mac:     mac,
		objects: make(map[uintptr]uint64),
		retired: make(map[uint64]bool),
	}, nil
}

// Mint 为一个新登记的对象签发带认证标签的指针。
// 每次 Mint 都分配全新代次；若地址此前属于其它对象，旧代次映射立即被覆盖，
// 旧标签在校验时会因代次不匹配而失效。
func (r *Registry) Mint(addr uintptr, kind Kind, ctx Context) (AuthenticatedPointer, error) {
	if addr == 0 {
		return AuthenticatedPointer{}, fmt.Errorf("%w: zero address", ErrInvalidPointer)
	}
	if _, err := kind.rank(); err != nil {
		return AuthenticatedPointer{}, err
	}
	if err := ctx.valid(); err != nil {
		return AuthenticatedPointer{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextGen++
	generation := r.nextGen
	r.objects[addr] = generation
	delete(r.retired, generation)

	return r.sealLocked(addr, generation, kind, ctx), nil
}

// Derive 从父指针派生出新地址的指针（如结构体内部指针、数组元素指针）。
// 派生指针必须重新认证：重新计算覆盖全部字段的标签，绝不复用父标签。
// 种类只允许保留或收窄，任何提权（如数据指针派生为函数指针）都会被拒绝。
// 父指针本身必须先通过 useCtx 下的完整使用门禁。
func (r *Registry) Derive(parent AuthenticatedPointer, newAddr uintptr, newKind Kind, useCtx Context) (AuthenticatedPointer, error) {
	if newAddr == 0 {
		return AuthenticatedPointer{}, fmt.Errorf("%w: zero address", ErrInvalidPointer)
	}
	newRank, err := newKind.rank()
	if err != nil {
		return AuthenticatedPointer{}, err
	}
	if err := useCtx.valid(); err != nil {
		return AuthenticatedPointer{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.verifyLocked(parent, useCtx); err != nil {
		return AuthenticatedPointer{}, fmt.Errorf("derive parent gate failed: %w", err)
	}

	parentRank, err := parent.kind.rank()
	if err != nil {
		return AuthenticatedPointer{}, err
	}
	if newRank > parentRank {
		return AuthenticatedPointer{}, fmt.Errorf("%w: %s -> %s", ErrWidenedKind, parent.kind, newKind)
	}

	// 派生指针与父对象同属一代，共享退役状态；登记派生地址使其受同一门禁约束。
	generation, ok := r.objects[parent.address]
	if !ok {
		return AuthenticatedPointer{}, fmt.Errorf("%w: parent address %#x", ErrUnknownObject, parent.address)
	}
	r.objects[newAddr] = generation

	return r.sealLocked(newAddr, generation, newKind, useCtx), nil
}

// Retire 将地址所属的当前对象代次标记为退役。
// 同一代次的所有指针（含派生内部指针）随后都无法通过任何使用门禁。
func (r *Registry) Retire(addr uintptr) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	generation, ok := r.objects[addr]
	if !ok {
		return fmt.Errorf("%w: %#x", ErrUnknownObject, addr)
	}
	r.retired[generation] = true
	return nil
}

// Verify 执行完整使用门禁：先校验认证标签，再核对代次、退役状态与调用上下文。
// 标签校验排在最前，未通过认证的字段不得驱动任何注册表状态判断。
func (r *Registry) Verify(p AuthenticatedPointer, ctx Context) error {
	if err := ctx.valid(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verifyLocked(p, ctx)
}

// UseForCall 是函数指针调用门禁：仅 KindFunction 且 Verify 通过时放行地址。
func (r *Registry) UseForCall(p AuthenticatedPointer, ctx Context) (uintptr, error) {
	if err := r.Verify(p, ctx); err != nil {
		return 0, err
	}
	if p.kind != KindFunction {
		return 0, fmt.Errorf("%w: kind %s rejected by call gate", ErrInvalidKind, p.kind)
	}
	return p.address, nil
}

// UseForDeref 是数据指针解引用门禁：仅 KindData 且 Verify 通过时放行地址。
func (r *Registry) UseForDeref(p AuthenticatedPointer, ctx Context) (uintptr, error) {
	if err := r.Verify(p, ctx); err != nil {
		return 0, err
	}
	if p.kind != KindData {
		return 0, fmt.Errorf("%w: kind %s rejected by dereference gate", ErrInvalidKind, p.kind)
	}
	return p.address, nil
}

// verifyLocked 要求调用方持有 r.mu。
func (r *Registry) verifyLocked(p AuthenticatedPointer, ctx Context) error {
	if p.address == 0 || len(p.tag) == 0 {
		return fmt.Errorf("%w: zero address or missing tag", ErrInvalidPointer)
	}

	expected := r.sealLocked(p.address, p.generation, p.kind, p.context).tag
	if !r.mac.Equal(expected, p.tag) {
		return ErrTagMismatch
	}

	generation, ok := r.objects[p.address]
	if !ok {
		return fmt.Errorf("%w: %#x", ErrUnknownObject, p.address)
	}
	if generation != p.generation {
		return fmt.Errorf("%w: address %#x now at generation %d, tag generation %d",
			ErrStaleGeneration, p.address, generation, p.generation)
	}
	if r.retired[generation] {
		return fmt.Errorf("%w: generation %d", ErrRetired, generation)
	}
	if p.context != ctx {
		return fmt.Errorf("%w: bound to domain %q call site %d",
			ErrContextMismatch, p.context.Domain, p.context.CallSite)
	}
	return nil
}

// sealLocked 用注入 MAC 对地址、代次、种类、调用上下文计算标签并封装指针。
func (r *Registry) sealLocked(addr uintptr, generation uint64, kind Kind, ctx Context) AuthenticatedPointer {
	input := encodePointerMaterial(addr, generation, kind, ctx)
	return AuthenticatedPointer{
		address:    addr,
		generation: generation,
		kind:       kind,
		context:    ctx,
		tag:        r.mac.Tag(input),
	}
}

// encodePointerMaterial 对标签覆盖的全部字段做确定性、长度自描述的二进制编码，
// 保证不同字段组合不会产生歧义碰撞。
func encodePointerMaterial(addr uintptr, generation uint64, kind Kind, ctx Context) []byte {
	domain := []byte(ctx.Domain)
	// 前缀 + 地址(8) + 代次(8) + 种类(1) + 域长度(2) + 域(n) + 调用点(8)
	buf := make([]byte, 0, len(tagDomainPrefix)+8+8+1+2+len(domain)+8)
	buf = append(buf, tagDomainPrefix...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(addr))
	buf = binary.BigEndian.AppendUint64(buf, generation)
	buf = append(buf, byte(kind))
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(domain)))
	buf = append(buf, domain...)
	buf = binary.BigEndian.AppendUint64(buf, ctx.CallSite)
	return buf
}
