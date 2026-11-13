package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// 实验约束上界。
const (
	MaxOrder           = 4096
	DefaultMaterialize = 10000
)

var (
	maxPrime = new(big.Int).Lsh(big.NewInt(1), 31) // p <= 2^31
	maxBound = new(big.Int).Lsh(big.NewInt(1), 32) // B <= 2^32
	bigOne   = big.NewInt(1)

	// ErrElementNotApproved 由修复回调在元素未获宿主批准时返回。
	ErrElementNotApproved = errors.New("netsec: peer element is not approved by the host")
	// ErrInvalidParams 表示实验参数不满足任务约束。
	ErrInvalidParams = errors.New("netsec: invalid handshake parameters")

	// hashFunc 可在测试中替换以模拟摘要碰撞；生产路径始终是 SHA-256。
	hashFunc = sha256.New
)

// 观测状态：每种异常都显式区分，绝不静默丢弃。
const (
	StatusUnique    = "unique-residue"
	StatusCollision = "multiple-matches"
	StatusNoMatch   = "no-match"
	StatusBadOrder  = "order-verification-failed"
	StatusRejected  = "element-rejected"
	StatusInvalid   = "invalid-params"
)

// HandshakeParams 描述一次握手实验：素数 p、非零元素 g、声称的精确阶 q 与挑战字节。
type HandshakeParams struct {
	P         *big.Int
	G         *big.Int
	Order     int
	Challenge []byte
}

// DigestCallback 是宿主提供的本地回调：返回 SHA-256(固定宽度大端(g^d mod p) || challenge)。
type DigestCallback func(hp HandshakeParams) ([]byte, error)

// ResidueMod 是一条同余信息 d ≡ Residue (mod Modulus)。
type ResidueMod struct {
	Residue *big.Int
	Modulus *big.Int
}

// Conflict 记录一对无法同时成立的同余及其来源实验。
type Conflict struct {
	ObservationID int
	Existing      ResidueMod
	Incoming      ResidueMod
	GCD           *big.Int
	Message       string
}

// MergeStep 保留一次广义 CRT 合并的完整推导，供重放与审计。
type MergeStep struct {
	ObservationID int
	Before        []ResidueMod
	Incoming      ResidueMod
	After         []ResidueMod
	Conflicts     []Conflict
}

// Observation 保留一次实验的全部输入、回调摘要、枚举结果与状态。
type Observation struct {
	ID        int
	Handshake HandshakeParams
	Width     int
	Digest    []byte
	Status    string
	// Residues 包含所有匹配的 k；StatusCollision 时长度大于 1。
	Residues []int
	Err      error
}

// Progression 是与全部观测相符的等差候选序列：r, r+m, ... 且均小于 B。
type Progression struct {
	First *big.Int
	Step  *big.Int
	Count *big.Int
}

// Result 是恢复/重放的完整结论。
type Result struct {
	Success      bool
	UniqueSecret *big.Int
	Progressions []Progression
	Candidates   []*big.Int
	Truncated    bool
	Conflicts    []Conflict
	MergeSteps   []MergeStep
	Observations []*Observation
}

// Session 在多次握手上累积观测并推导静态指数 d 的候选集合。
type Session struct {
	B                *big.Int
	cb               DigestCallback
	observations     []*Observation
	MaterializeLimit int
}

// NewSession 校验 B（0 < B <= 2^32）与回调后创建会话。
func NewSession(B *big.Int, cb DigestCallback) (*Session, error) {
	if B == nil || B.Sign() <= 0 || B.Cmp(maxBound) > 0 {
		return nil, fmt.Errorf("%w: B must satisfy 0 < B <= 2^32", ErrInvalidParams)
	}
	if cb == nil {
		return nil, fmt.Errorf("%w: nil callback", ErrInvalidParams)
	}
	return &Session{B: new(big.Int).Set(B), cb: cb, MaterializeLimit: DefaultMaterialize}, nil
}

// Observations 返回至今保留的全部观测（含挑战与摘要），可直接用于 Replay。
func (s *Session) Observations() []*Observation {
	out := make([]*Observation, len(s.observations))
	copy(out, s.observations)
	return out
}

// Observe 执行一次握手实验：先验证 g 的阶恰为 q，再枚举 0..q-1 匹配摘要。
// 任何拒绝或校验失败都只记录状态，不会产生同余信息。
func (s *Session) Observe(id int, hp HandshakeParams) *Observation {
	obs := &Observation{ID: id, Handshake: copyHandshake(hp), Width: byteLen(hp.P)}
	if err := ValidateParams(&hp); err != nil {
		obs.Status, obs.Err = StatusInvalid, err
		s.observations = append(s.observations, obs)
		return obs
	}
	obs.Width = byteLen(hp.P)
	if err := ExactOrder(hp.P, hp.G, hp.Order); err != nil {
		obs.Status, obs.Err = StatusBadOrder, err
		s.observations = append(s.observations, obs)
		return obs
	}
	digest, err := s.cb(hp)
	if err != nil {
		obs.Status, obs.Err = StatusRejected, err
		s.observations = append(s.observations, obs)
		return obs
	}
	obs.Digest = append([]byte(nil), digest...)
	obs.Residues = enumerateResidues(hp, digest)
	switch len(obs.Residues) {
	case 0:
		obs.Status = StatusNoMatch
	case 1:
		obs.Status = StatusUnique
	default:
		obs.Status = StatusCollision
	}
	s.observations = append(s.observations, obs)
	return obs
}

// Derive 用广义中国剩余定理合并当前全部观测并物化候选集合。
func (s *Session) Derive() *Result {
	return deriveFrom(s.B, s.observations, s.MaterializeLimit)
}

// Replay 仅依赖保留下来的观测与挑战重新推导，不需要回调；limit<=0 用默认值。
func Replay(B *big.Int, observations []*Observation, materializeLimit int) (*Result, error) {
	if B == nil || B.Sign() <= 0 || B.Cmp(maxBound) > 0 {
		return nil, fmt.Errorf("%w: B must satisfy 0 < B <= 2^32", ErrInvalidParams)
	}
	if materializeLimit <= 0 {
		materializeLimit = DefaultMaterialize
	}
	return deriveFrom(new(big.Int).Set(B), observations, materializeLimit), nil
}

// ValidateParams 检查结构约束：p 为不超过 2^31 的素数，g 为模 p 非零元素，1 <= q <= 4096。
func ValidateParams(hp *HandshakeParams) error {
	if hp == nil || hp.P == nil || hp.G == nil {
		return fmt.Errorf("%w: nil p or g", ErrInvalidParams)
	}
	if hp.P.Sign() <= 0 || hp.P.Cmp(maxPrime) > 0 {
		return fmt.Errorf("%w: p must be a prime with p <= 2^31", ErrInvalidParams)
	}
	if !hp.P.ProbablyPrime(20) {
		return fmt.Errorf("%w: p is not prime", ErrInvalidParams)
	}
	if hp.G.Sign() <= 0 || hp.G.Cmp(hp.P) >= 0 {
		return fmt.Errorf("%w: g must satisfy 1 <= g < p", ErrInvalidParams)
	}
	if hp.Order < 1 || hp.Order > MaxOrder {
		return fmt.Errorf("%w: q must satisfy 1 <= q <= 4096", ErrInvalidParams)
	}
	return nil
}

// ExactOrder 验证 g 在模 p 下的阶恰好为 q：g^q=1 且对 q 的每个素因子 r，g^(q/r)!=1。
func ExactOrder(p, g *big.Int, q int) error {
	if err := ValidateParams(&HandshakeParams{P: p, G: g, Order: q, Challenge: []byte{}}); err != nil {
		return err
	}
	qb := big.NewInt(int64(q))
	if new(big.Int).Exp(g, qb, p).Cmp(bigOne) != 0 {
		return fmt.Errorf("%w: g^%d != 1 mod p", ErrInvalidParams, q)
	}
	for _, factor := range primeFactors(q) {
		exp := new(big.Int).Quo(qb, big.NewInt(int64(factor)))
		if new(big.Int).Exp(g, exp, p).Cmp(bigOne) == 0 {
			return fmt.Errorf("%w: g^(q/%d) == 1, order is a proper divisor of q", ErrInvalidParams, factor)
		}
	}
	return nil
}

// HandshakeDigest 计算 SHA-256(fixedWidthBE(shared, 字节长度(p)) || challenge)。
func HandshakeDigest(p, shared *big.Int, challenge []byte) []byte {
	width := byteLen(p)
	buf := make([]byte, width+len(challenge))
	shared.FillBytes(buf[:width])
	copy(buf[width:], challenge)
	h := hashFunc()
	h.Write(buf)
	return h.Sum(nil)
}

func byteLen(p *big.Int) int {
	if p == nil {
		return 0
	}
	return (p.BitLen() + 7) / 8
}

func copyHandshake(hp HandshakeParams) HandshakeParams {
	out := hp
	if hp.P != nil {
		out.P = new(big.Int).Set(hp.P)
	}
	if hp.G != nil {
		out.G = new(big.Int).Set(hp.G)
	}
	if hp.Challenge != nil {
		out.Challenge = append([]byte(nil), hp.Challenge...)
	}
	return out
}

func primeFactors(n int) []int {
	factors := make([]int, 0)
	for factor := 2; factor*factor <= n; factor++ {
		if n%factor == 0 {
			factors = append(factors, factor)
			for n%factor == 0 {
				n /= factor
			}
		}
	}
	if n > 1 {
		factors = append(factors, n)
	}
	return factors
}

// VulnerableCallback 构造只做基本边界检查、不验证元素阶也不做批准的回调（脆弱基线）。
func VulnerableCallback(d *big.Int) DigestCallback {
	secret := new(big.Int).Set(d)
	return func(hp HandshakeParams) ([]byte, error) {
		if err := basicChecks(&hp); err != nil {
			return nil, err
		}
		shared := new(big.Int).Exp(hp.G, secret, hp.P)
		return HandshakeDigest(hp.P, shared, hp.Challenge), nil
	}
}

// ApprovalSet 是宿主维护的已批准元素白名单（按 p、g、声称阶三元组批准）。
type ApprovalSet struct {
	mu      sync.RWMutex
	allowed map[string]approvedEntry
}

type approvedEntry struct {
	p     *big.Int
	g     *big.Int
	order int
}

// NewApprovalSet 创建空白名单。
func NewApprovalSet() *ApprovalSet {
	return &ApprovalSet{allowed: make(map[string]approvedEntry)}
}

// Allow 在批准前强制做参数与精确阶校验，伪造阶的元素无法进入白名单。
func (a *ApprovalSet) Allow(hp HandshakeParams) error {
	if err := ValidateParams(&hp); err != nil {
		return err
	}
	if err := ExactOrder(hp.P, hp.G, hp.Order); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowed[approvalKey(hp)] = approvedEntry{
		p:     new(big.Int).Set(hp.P),
		g:     new(big.Int).Set(hp.G),
		order: hp.Order,
	}
	return nil
}

func (a *ApprovalSet) isApproved(hp *HandshakeParams) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	entry, ok := a.allowed[approvalKey(*hp)]
	return ok && entry.order == hp.Order
}

func approvalKey(hp HandshakeParams) string {
	return fmt.Sprintf("%x|%x|%d", hp.P.Bytes(), hp.G.Bytes(), hp.Order)
}

// HardenedCallback 构造修复回调：拒绝任何未获宿主批准的元素，拒绝直接返回错误、不产生同余。
func HardenedCallback(d *big.Int, approvals *ApprovalSet) (DigestCallback, error) {
	if d == nil || d.Sign() < 0 {
		return nil, fmt.Errorf("%w: secret d must be a non-negative integer", ErrInvalidParams)
	}
	if approvals == nil {
		return nil, fmt.Errorf("%w: nil approval set", ErrInvalidParams)
	}
	secret := new(big.Int).Set(d)
	return func(hp HandshakeParams) ([]byte, error) {
		if err := basicChecks(&hp); err != nil {
			return nil, err
		}
		if err := ExactOrder(hp.P, hp.G, hp.Order); err != nil {
			return nil, err
		}
		if !approvals.isApproved(&hp) {
			return nil, fmt.Errorf("%w: order=%d", ErrElementNotApproved, hp.Order)
		}
		shared := new(big.Int).Exp(hp.G, secret, hp.P)
		return HandshakeDigest(hp.P, shared, hp.Challenge), nil
	}, nil
}

func basicChecks(hp *HandshakeParams) error {
	if hp == nil || hp.P == nil || hp.G == nil {
		return fmt.Errorf("%w: nil p or g", ErrInvalidParams)
	}
	if hp.P.Sign() <= 0 || hp.P.Cmp(maxPrime) > 0 {
		return fmt.Errorf("%w: p must satisfy p <= 2^31", ErrInvalidParams)
	}
	if hp.G.Sign() <= 0 || hp.G.Cmp(hp.P) >= 0 {
		return fmt.Errorf("%w: g must satisfy 1 <= g < p", ErrInvalidParams)
	}
	if hp.Order < 1 || hp.Order > MaxOrder {
		return fmt.Errorf("%w: q must satisfy 1 <= q <= 4096", ErrInvalidParams)
	}
	return nil
}

// enumerateResidues 逐个计算 g^k（0<=k<q）并比对回调摘要；保留全部匹配，绝不只取首个。
func enumerateResidues(hp HandshakeParams, want []byte) []int {
	matches := make([]int, 0)
	gk := big.NewInt(1)
	for k := 0; k < hp.Order; k++ {
		got := HandshakeDigest(hp.P, gk, hp.Challenge)
		if len(got) == len(want) && bytes.Equal(got, want) {
			matches = append(matches, k)
		}
		gk.Mul(gk, hp.G).Mod(gk, hp.P)
	}
	return matches
}

type congruence struct {
	r, mod *big.Int
}

// mergeCRT 广义中国剩余定理：合并 d≡r1 (mod m1) 与 d≡r2 (mod m2)，模数不必互素。
// 无解（(r2-r1) 不被 gcd(m1,m2) 整除）时 ok=false，由调用方记录冲突实验而非相乘模数。
func mergeCRT(r1, m1, r2, m2 *big.Int) (r, mod *big.Int, ok bool) {
	g := new(big.Int).GCD(nil, nil, m1, m2)
	diff := new(big.Int).Sub(r2, r1)
	if new(big.Int).Mod(diff, g).Sign() != 0 {
		return nil, nil, false
	}
	lcm := new(big.Int).Quo(new(big.Int).Mul(m1, m2), g)

	// 令 m1'=m1/g, m2'=m2/g；解 r1 + m1*t ≡ r2 (mod m2)，gcd(m1',m2')=1。
	m1p := new(big.Int).Quo(m1, g)
	m2p := new(big.Int).Quo(m2, g)
	diffQuo := new(big.Int).Quo(diff, g)
	inv := new(big.Int).ModInverse(m1p, m2p)
	if inv == nil {
		return nil, nil, false
	}
	t := new(big.Int).Mul(diffQuo, inv)
	t.Mod(t, m2p)
	merged := new(big.Int).Add(r1, new(big.Int).Mul(m1, t))
	merged.Mod(merged, lcm)
	return merged, lcm, true
}

func deriveFrom(B *big.Int, observations []*Observation, materializeLimit int) *Result {
	result := &Result{Observations: observations}
	branches := make([]congruence, 0)

	for _, obs := range observations {
		var residues []int
		switch obs.Status {
		case StatusUnique, StatusCollision:
			residues = obs.Residues
		default:
			// no-match、阶校验失败、回调拒绝、非法参数：均不产生同余。
			continue
		}
		step := MergeStep{ObservationID: obs.ID, Before: exportBranches(branches)}
		next := make([]congruence, 0, len(branches)*len(residues)+len(residues))
		for _, k := range residues {
			incoming := congruence{r: big.NewInt(int64(k)), mod: big.NewInt(int64(obs.Handshake.Order))}
			step.Incoming = ResidueMod{Residue: new(big.Int).Set(incoming.r), Modulus: new(big.Int).Set(incoming.mod)}
			if len(branches) == 0 {
				next = append(next, incoming)
				continue
			}
			for _, existing := range branches {
				merged, lcm, ok := mergeCRT(existing.r, existing.mod, incoming.r, incoming.mod)
				if !ok {
					g := new(big.Int).GCD(nil, nil, existing.mod, incoming.mod)
					conflict := Conflict{
						ObservationID: obs.ID,
						Existing:      ResidueMod{Residue: new(big.Int).Set(existing.r), Modulus: new(big.Int).Set(existing.mod)},
						Incoming:      ResidueMod{Residue: new(big.Int).Set(incoming.r), Modulus: new(big.Int).Set(incoming.mod)},
						GCD:           g,
						Message: fmt.Sprintf(
							"observation #%d residue d ≡ %d (mod %d) conflicts with d ≡ %d (mod %d): %d not divisible by gcd %d",
							obs.ID, incoming.r, incoming.mod, existing.r, existing.mod,
							new(big.Int).Abs(new(big.Int).Sub(incoming.r, existing.r)), g),
					}
					result.Conflicts = append(result.Conflicts, conflict)
					step.Conflicts = append(step.Conflicts, conflict)
					continue
				}
				next = append(next, congruence{r: merged, mod: lcm})
			}
		}
		branches = dedupeBranches(next)
		step.After = exportBranches(branches)
		result.MergeSteps = append(result.MergeSteps, step)
	}

	if len(result.Conflicts) > 0 {
		// 存在矛盾时不存在与全部观测相符的候选，不返回任何“恢复”结果。
		return result
	}
	if len(branches) == 0 {
		// 没有可用同余：与所有观测相符的范围是整个 0..B-1。
		branches = []congruence{{r: big.NewInt(0), mod: big.NewInt(1)}}
	}

	limit := big.NewInt(int64(materializeLimit))
	for _, br := range branches {
		r := new(big.Int).Set(br.r)
		first := new(big.Int).Mod(r, br.mod)
		count := new(big.Int).Sub(B, first)
		if count.Sign() <= 0 {
			result.Progressions = append(result.Progressions, Progression{
				First: new(big.Int).Set(first), Step: new(big.Int).Set(br.mod), Count: big.NewInt(0),
			})
			continue
		}
		count = addOne(new(big.Int).Quo(new(big.Int).Sub(count, bigOne), br.mod))
		result.Progressions = append(result.Progressions, Progression{
			First: new(big.Int).Set(first), Step: new(big.Int).Set(br.mod), Count: new(big.Int).Set(count),
		})
		if count.Cmp(limit) > 0 {
			result.Truncated = true
			continue
		}
		for cand := new(big.Int).Set(first); cand.Cmp(B) < 0; cand.Add(cand, br.mod) {
			result.Candidates = append(result.Candidates, new(big.Int).Set(cand))
		}
	}
	sortBigInts(result.Candidates)
	if len(result.Candidates) == 1 {
		result.Success = true
		result.UniqueSecret = result.Candidates[0]
	}
	return result
}

func addOne(x *big.Int) *big.Int { return x.Add(x, bigOne) }

func dedupeBranches(in []congruence) []congruence {
	seen := make(map[string]bool)
	out := make([]congruence, 0, len(in))
	for _, br := range in {
		key := br.r.String() + "/" + br.mod.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, congruence{r: new(big.Int).Set(br.r), mod: new(big.Int).Set(br.mod)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].mod.Cmp(out[j].mod) != 0 {
			return out[i].mod.Cmp(out[j].mod) < 0
		}
		return out[i].r.Cmp(out[j].r) < 0
	})
	return out
}

func exportBranches(branches []congruence) []ResidueMod {
	out := make([]ResidueMod, 0, len(branches))
	for _, br := range branches {
		out = append(out, ResidueMod{Residue: new(big.Int).Set(br.r), Modulus: new(big.Int).Set(br.mod)})
	}
	return out
}

func sortBigInts(values []*big.Int) {
	sort.Slice(values, func(i, j int) bool { return values[i].Cmp(values[j]) < 0 })
}
