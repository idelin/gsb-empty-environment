package pseudonym

import (
	"crypto/subtle"
	"errors"
	"sort"
	"sync"
	"time"
)

// AliasKind 描述显式别名关系的来源。
type AliasKind string

const (
	// AliasCollision 表示因短码碰撞而产生的扩展关系：
	// identity 的旧事件可能仍挂在最初发布的最短碰撞码下。
	AliasCollision AliasKind = "collision_extension"

	// AliasLegacy 表示由历史脱敏数据迁移而来的人工别名。
	AliasLegacy AliasKind = "legacy_migration"
)

// Alias 记录“某个身份可以通过某个旧短码查到”的显式关系。
type Alias struct {
	Identity string    `json:"identity"`
	Code     string    `json:"code"`
	Kind     AliasKind `json:"kind"`
	Created  time.Time `json:"created"`
}

// Record 是一个已分配伪名的完整内部状态。短码是索引，完整摘要才是身份凭据。
type Record struct {
	Identity string    `json:"identity"`
	Code     string    `json:"code"`
	Digest   []byte    `json:"digest"`
	Created  time.Time `json:"created"`
}

// Entry 是 WAL 中的单条记录。同一时刻只有一种载荷非空。
type Entry struct {
	Binding *Record `json:"binding,omitempty"`
	Alias   *Alias  `json:"alias,omitempty"`
}

func (e Entry) clone() Entry {
	out := Entry{}
	if e.Binding != nil {
		binding := *e.Binding
		binding.Digest = append([]byte(nil), e.Binding.Digest...)
		out.Binding = &binding
	}
	if e.Alias != nil {
		alias := *e.Alias
		out.Alias = &alias
	}
	return out
}

var (
	// ErrEmptyIdentity 表示传入了空标识符。
	ErrEmptyIdentity = errors.New("pseudonym: identity must not be empty")
	// ErrCodeSpaceExhausted 表示完整摘要的所有前缀都已被占用，无法继续扩展。
	ErrCodeSpaceExhausted = errors.New("pseudonym: code space exhausted")
	// ErrUnknownCode 表示没有任何身份或别名引用该短码。
	ErrUnknownCode = errors.New("pseudonym: unknown code")
	// ErrConflict 表示回放或分配时发现持久化记录与内存状态不一致。
	ErrConflict = errors.New("pseudonym: conflicting log entry")
	// ErrUnknownIdentity 表示该标识符从未分配过伪名。
	ErrUnknownIdentity = errors.New("pseudonym: unknown identity")
)

// Option 配置 Allocator。
type Option func(*Allocator)

// WithMinCodeLen 设置最短发布码长度，默认 8，下限为 1。
// 碰撞时只会在此基础上向更长的前缀扩展。
func WithMinCodeLen(n int) Option {
	return func(a *Allocator) {
		if n >= 1 {
			a.minCodeLen = n
		}
	}
}

// WithClock 注入时间源，主要用于确定性测试。
func WithClock(now func() time.Time) Option {
	return func(a *Allocator) {
		if now != nil {
			a.now = now
		}
	}
}

// Allocator 负责伪名的并发分配、身份确认与别名查询。
// 所有公开方法均可被多个 goroutine 同时调用。
type Allocator struct {
	store      Store
	digester   Digester
	minCodeLen int
	now        func() time.Time

	mu sync.Mutex

	// byCode 以已发布短码为主索引；短码只用于查找，绝不用于判定身份。
	byCode map[string]*Record
	// byIdentity 保证同一标识符重复分配时幂等返回原伪名。
	byIdentity map[string]*Record
	// aliases 是 code -> 显式别名集合；别名保证旧事件仍可按旧短码查询。
	aliases map[string][]Alias
	// aliasSeen 以 identity\x00code 去重，避免重复写入相同别名。
	aliasSeen map[string]struct{}
}

// New 创建分配器。store 同时实现 Loader 时会回放历史记录完成重启恢复。
func New(store Store, digester Digester, opts ...Option) (*Allocator, error) {
	if digester == nil {
		panic("pseudonym: digester is required")
	}
	a := &Allocator{
		store:      store,
		digester:   digester,
		minCodeLen: 8,
		now:        time.Now,
		byCode:     make(map[string]*Record),
		byIdentity: make(map[string]*Record),
		aliases:    make(map[string][]Alias),
		aliasSeen:  make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.minCodeLen > maxCodeChars {
		a.minCodeLen = maxCodeChars
	}

	if loader, ok := store.(Loader); ok {
		entries, _, err := loader.Load()
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := a.replay(entry); err != nil {
				return nil, err
			}
		}
	}
	return a, nil
}

// Allocate 为标识符分配伪名，整个过程在互斥区内原子完成。
//
// 规则：
//   - 同一标识符重复调用时幂等返回已发布的短码，已发布短码永不改变；
//   - 新标识符从最短前缀开始尝试，遇到碰撞就扩展一个字符，直到找到
//     未被占用的前缀；最初的碰撞码登记为该身份的显式别名；
//   - 完整摘要与短码一起持久化，后续身份确认只认完整 HMAC。
func (a *Allocator) Allocate(identity string) (code string, err error) {
	if identity == "" {
		return "", ErrEmptyIdentity
	}

	digest := a.digester.Digest(identity)
	encoded := encodeDigest(digest)

	a.mu.Lock()
	defer a.mu.Unlock()

	if existing := a.byIdentity[identity]; existing != nil {
		return existing.Code, nil
	}

	chosen, collided, err := a.pickCode(encoded)
	if err != nil {
		return "", err
	}

	record := &Record{
		Identity: identity,
		Code:     chosen,
		Digest:   append([]byte(nil), digest...),
		Created:  a.now().UTC(),
	}
	if err := a.store.Append(Entry{Binding: record}); err != nil {
		return "", err
	}
	a.byCode[chosen] = record
	a.byIdentity[identity] = record

	// 只有最初发布的最短码（collided 的第一个前缀）下可能存在该身份
	// 截短碰撞时期的旧事件；中间被跳过的前缀属于他人的已发布短码，
	// 本身份从未在那里写过事件，因此不挂别名，避免查询时误并。
	if len(collided) > 0 {
		if err := a.appendAliasLocked(Alias{
			Identity: identity,
			Code:     collided[0],
			Kind:     AliasCollision,
			Created:  record.Created,
		}); err != nil {
			return "", err
		}
	}
	return chosen, nil
}

// pickCode 返回未被占用的最短前缀，以及途中被他人占用而跳过的前缀。
// 调用方必须持有 mu。
func (a *Allocator) pickCode(encoded string) (string, []string, error) {
	var collided []string
	for n := a.minCodeLen; n <= maxCodeChars && n <= len(encoded); n++ {
		candidate := encoded[:n]
		if _, taken := a.byCode[candidate]; taken {
			collided = append(collided, candidate)
			continue
		}
		return candidate, collided, nil
	}
	return "", nil, ErrCodeSpaceExhausted
}

// Confirm 用完整 HMAC 摘要确认短码持有者的身份，比较采用恒定时间。
// 短码命中只完成索引查找；摘要不匹配时绝不透露真实持有者信息。
func (a *Allocator) Confirm(code string, digest []byte) bool {
	a.mu.Lock()
	record := a.byCode[code]
	a.mu.Unlock()
	if record == nil || len(record.Digest) != len(digest) {
		return false
	}
	return subtle.ConstantTimeCompare(record.Digest, digest) == 1
}

// Resolve 返回所有可通过该短码查询到的身份。
//
// 短码的当前持有者排在首位；碰撞扩展或历史迁移产生的显式别名随后返回。
// 因此旧事件按旧短码检索时，不会被错误地合并到当前持有者身上。
func (a *Allocator) Resolve(code string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	record := a.byCode[code]
	aliases := a.aliases[code]
	if record == nil && len(aliases) == 0 {
		return nil, ErrUnknownCode
	}

	seen := make(map[string]struct{}, len(aliases)+1)
	out := make([]string, 0, len(aliases)+1)
	if record != nil {
		out = append(out, record.Identity)
		seen[record.Identity] = struct{}{}
	}
	sortedAliases := append([]Alias(nil), aliases...)
	sort.Slice(sortedAliases, func(i, j int) bool {
		if sortedAliases[i].Identity != sortedAliases[j].Identity {
			return sortedAliases[i].Identity < sortedAliases[j].Identity
		}
		return sortedAliases[i].Kind < sortedAliases[j].Kind
	})
	for _, alias := range sortedAliases {
		if _, ok := seen[alias.Identity]; ok {
			continue
		}
		seen[alias.Identity] = struct{}{}
		out = append(out, alias.Identity)
	}
	return out, nil
}

// Lookup 返回标识符当前已发布的伪名；已发布短码永远不会被更换。
func (a *Allocator) Lookup(identity string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.byIdentity[identity]
	if record == nil {
		return "", ErrUnknownIdentity
	}
	return record.Code, nil
}

// Digest 返回标识符的完整 HMAC 摘要副本，供调用方写入事件并日后 Confirm。
func (a *Allocator) Digest(identity string) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.byIdentity[identity]
	if record == nil {
		return nil, ErrUnknownIdentity
	}
	return append([]byte(nil), record.Digest...), nil
}

// AddAlias 登记历史迁移别名：identity 的旧事件可以通过 legacyCode 查到。
// 该接口用于把截短碰撞时期遗留的事件纳入查询，不改变任何已发布短码。
func (a *Allocator) AddAlias(identity, legacyCode string, kind AliasKind) error {
	if identity == "" || legacyCode == "" {
		return ErrEmptyIdentity
	}
	if kind != AliasCollision && kind != AliasLegacy {
		return errors.New("pseudonym: unknown alias kind")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendAliasLocked(Alias{
		Identity: identity,
		Code:     legacyCode,
		Kind:     kind,
		Created:  a.now().UTC(),
	})
}

// AliasesOf 返回某短码下登记的全部显式别名副本。
func (a *Allocator) AliasesOf(code string) []Alias {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.aliases[code]
	if len(src) == 0 {
		return nil
	}
	out := make([]Alias, len(src))
	copy(out, src)
	return out
}

// appendAliasLocked 追加并应用别名，调用方必须持有 mu。
func (a *Allocator) appendAliasLocked(alias Alias) error {
	key := alias.Identity + "\x00" + alias.Code
	if _, ok := a.aliasSeen[key]; ok {
		return nil
	}
	stored := alias
	if err := a.store.Append(Entry{Alias: &stored}); err != nil {
		return err
	}
	a.aliases[alias.Code] = append(a.aliases[alias.Code], alias)
	a.aliasSeen[key] = struct{}{}
	return nil
}

// replay 将一条历史记录应用到内存索引，并校验与既有状态不冲突。
func (a *Allocator) replay(entry Entry) error {
	switch {
	case entry.Binding != nil:
		return a.replayBinding(*entry.Binding)
	case entry.Alias != nil:
		return a.replayAlias(*entry.Alias)
	default:
		return ErrConflict
	}
}

func (a *Allocator) replayBinding(record Record) error {
	if record.Identity == "" || record.Code == "" || len(record.Digest) == 0 {
		return ErrConflict
	}
	if existing := a.byIdentity[record.Identity]; existing != nil {
		if existing.Code != record.Code || !equalBytes(existing.Digest, record.Digest) {
			return ErrConflict
		}
		return nil
	}
	if _, taken := a.byCode[record.Code]; taken {
		return ErrConflict
	}
	recordCopy := record
	recordCopy.Digest = append([]byte(nil), record.Digest...)
	a.byCode[record.Code] = &recordCopy
	a.byIdentity[record.Identity] = &recordCopy
	return nil
}

func (a *Allocator) replayAlias(alias Alias) error {
	if alias.Identity == "" || alias.Code == "" {
		return ErrConflict
	}
	key := alias.Identity + "\x00" + alias.Code
	if _, ok := a.aliasSeen[key]; ok {
		return nil
	}
	a.aliases[alias.Code] = append(a.aliases[alias.Code], alias)
	a.aliasSeen[key] = struct{}{}
	return nil
}

func equalBytes(x, y []byte) bool {
	if len(x) != len(y) {
		return false
	}
	return subtle.ConstantTimeCompare(x, y) == 1
}
