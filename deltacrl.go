// Package deltacrl 维护证书校验服务的 CRL 吊销视图。
//
// 服务每天拉取一份完整（基础）CRL，每小时只拉增量 CRL（delta CRL，
// RFC 5280 §5.2.4）。Checker 负责基础 CRL 与增量 CRL 的接入、校验、
// 合并，并对外给出每张证书的吊销状态。
package deltacrl

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"math/big"
	"sync"
	"time"
)

// RFC 5280 §5.3.1 的吊销原因枚举。
const (
	ReasonUnspecified        = 0
	ReasonKeyCompromise      = 1
	ReasonCACompromise       = 2
	ReasonAffiliationChange  = 3
	ReasonSuperseded         = 4
	ReasonCessationOfOp      = 5
	ReasonCertificateHold    = 6
	ReasonRemoveFromCRL      = 8
	ReasonPrivilegeWithdrawn = 9
	ReasonAACompromise       = 10
)

var (
	oidExtCRLNumber         = asn1.ObjectIdentifier{2, 5, 29, 20}
	oidExtDeltaCRLIndicator = asn1.ObjectIdentifier{2, 5, 29, 27}
	oidExtIssuingDP         = asn1.ObjectIdentifier{2, 5, 29, 28}
)

var (
	// ErrBadSignature 表示 CRL 签名无法用签发者证书验证通过（内容被篡改或不是该 CA 签发）。
	ErrBadSignature = errors.New("deltacrl: CRL signature verification failed")
	// ErrIssuerMismatch 表示 CRL 的签发者与当前信任的 CRL 签发者不一致。
	ErrIssuerMismatch = errors.New("deltacrl: CRL issuer does not match the trusted CRL issuer")
	// ErrMissingCRLNumber 表示 CRL 缺少 cRLNumber 扩展，无法参与序号比较。
	ErrMissingCRLNumber = errors.New("deltacrl: CRL is missing the cRLNumber extension")
	// ErrMalformedExtension 表示增量指示符或签发分发点等关键扩展编码异常。
	ErrMalformedExtension = errors.New("deltacrl: malformed CRL extension")
	// ErrStaleCRL 表示到达的 CRL 序号不大于已持有的同范围 CRL，属于过期重放。
	ErrStaleCRL = errors.New("deltacrl: stale CRL number")
	// ErrNoBaseCRL 表示尚无基础 CRL，增量 CRL 无法落地，只能暂存等待基础 CRL。
	ErrNoBaseCRL = errors.New("deltacrl: no base CRL available yet")
	// ErrFutureBaseReference 表示增量 CRL 的 baseCRLNumber 晚于当前基础 CRL 的 cRLNumber。
	ErrFutureBaseReference = errors.New("deltacrl: delta CRL references a base CRL newer than the current base")
	// ErrScopeMismatch 表示增量 CRL 与基础 CRL 的签发者或分发点范围不一致。
	ErrScopeMismatch = errors.New("deltacrl: delta CRL scope does not match the base CRL scope")
	// ErrInvalidRemoveFromCRL 表示 removeFromCRL 试图解除一个并非 certificateHold 的吊销。
	ErrInvalidRemoveFromCRL = errors.New("deltacrl: removeFromCRL entry does not correspond to a held certificate")
	// ErrExpired 表示参与判定的基础 CRL 或任一增量 CRL 已过 nextUpdate，状态无法确定。
	ErrExpired = errors.New("deltacrl: a CRL in the validation path has expired")
)

// Status 是证书的吊销状态三值枚举（RFC 5280 §5.3.3）。
type Status int

const (
	// Unknown 表示无法给出确定结论，例如 CRL 已过期或尚无 CRL 数据。
	Unknown Status = iota
	// Good 表示证书在当前 CRL 范围内有效（未被吊销，或暂停已被解除）。
	Good
	// Revoked 表示证书已被吊销（含 certificateHold 暂停）。
	Revoked
)

func (s Status) String() string {
	switch s {
	case Good:
		return "good"
	case Revoked:
		return "revoked"
	default:
		return "unknown"
	}
}

// Result 是一次吊销查询的结果。
type Result struct {
	Status Status
	// Reason 为 RFC 5280 §5.3.1 的原因码；Good/Unknown 时为 0。
	Reason int
	// RevokedAt 为吊销生效时间；Good/Unknown 时为零值。
	RevokedAt time.Time
}

// scope 描述一份 CRL 的覆盖范围，取自 issuingDistributionPoint 扩展（RFC 5280 §5.2.5）。
type scope struct {
	// present 表示 CRL 是否携带 IDP 扩展。
	present bool
	// fullName 是分发点 fullName（GeneralNames）的 DER 编码；无 IDP 时为 nil。
	fullName []byte
	// onlyUserCerts / onlyCACerts / onlySomeReasons / indirectCRL 对应 IDP 各字段。
	onlyUserCerts   bool
	onlyCACerts     bool
	onlySomeReasons asn1.BitString
	indirectCRL     bool
	// relative 表示使用了 nameRelativeToCRLIssuer（本实现不支持跨范围合并）。
	relative bool
}

type asn1IDP struct {
	DistributionPoint asn1DPWrapper  `asn1:"optional,tag:0,explicit"`
	OnlyUserCerts     bool           `asn1:"optional,tag:1,implicit"`
	OnlyCACerts       bool           `asn1:"optional,tag:2,implicit"`
	OnlySomeReasons   asn1.BitString `asn1:"optional,tag:3,implicit"`
	IndirectCRL       bool           `asn1:"optional,tag:4,implicit"`
}

// asn1DPWrapper 承载 [0] EXPLICIT 包装；内部才是 DistributionPointName SEQUENCE。
type asn1DPWrapper struct {
	FullName []asn1.RawValue
}

type crlHolder struct {
	rl    *x509.RevocationList
	scope scope
}

// Checker 持有一个 CRL 范围（一个签发者 + 一个分发点）的基础 CRL、
// 已接受的增量 CRL 序列，以及按 RFC 5280 §5.3.3 合并出的吊销视图。
type Checker struct {
	issuer *x509.Certificate
	now    func() time.Time

	mu      sync.Mutex
	base    *crlHolder
	deltas  []*crlHolder // 已接受的增量，按 cRLNumber 升序
	pending []*crlHolder // 基础 CRL 缺失时暂存的增量，按 cRLNumber 升序
	merged  map[string]revokedState
}

type revokedState struct {
	reason int
	at     time.Time
}

// NewChecker 创建针对单个 CRL 签发者的吊销状态组件。
// issuer 必须是 CRL 签发 CA 证书（带 CRL 签名密钥用法），所有接入的
// 基础/增量 CRL 都必须由它签发。
func NewChecker(issuer *x509.Certificate) *Checker {
	return &Checker{
		issuer: issuer,
		now:    time.Now,
		merged: make(map[string]revokedState),
	}
}

// WithClock 注入自定义时钟，主要用于测试过期行为。
func (c *Checker) WithClock(now func() time.Time) *Checker {
	c.now = now
	return c
}

// IngestDER 接入一份 DER 编码的 CRL（基础或增量），完成全部接入校验并更新吊销视图。
func (c *Checker) IngestDER(der []byte) error {
	rl, err := x509.ParseRevocationList(der)
	if err != nil {
		return err
	}
	if err := rl.CheckSignatureFrom(c.issuer); err != nil {
		return ErrBadSignature
	}
	if !bytesEqual(rl.RawIssuer, c.issuer.RawSubject) {
		return ErrIssuerMismatch
	}

	number, isDelta, baseRef, err := classifyCRL(rl)
	if err != nil {
		return err
	}
	sc, err := parseScope(rl)
	if err != nil {
		return err
	}

	holder := &crlHolder{rl: rl, scope: sc}

	c.mu.Lock()
	defer c.mu.Unlock()

	if isDelta {
		return c.ingestDeltaLocked(holder, number, baseRef)
	}
	return c.ingestBaseLocked(holder, number)
}

// classifyCRL 解析 cRLNumber 与 deltaCRLIndicator，返回 (序号, 是否增量, baseCRLNumber, 错误)。
func classifyCRL(rl *x509.RevocationList) (number *big.Int, isDelta bool, baseRef *big.Int, err error) {
	var rawNumber []byte
	for _, ext := range rl.Extensions {
		switch {
		case ext.Id.Equal(oidExtCRLNumber):
			rawNumber = ext.Value
		case ext.Id.Equal(oidExtDeltaCRLIndicator):
			isDelta = true
			n, e := decodeASN1Integer(ext.Value)
			if e != nil {
				return nil, false, nil, ErrMalformedExtension
			}
			baseRef = n
		}
	}
	if rawNumber == nil {
		return nil, false, nil, ErrMissingCRLNumber
	}
	number, err = decodeASN1Integer(rawNumber)
	if err != nil {
		return nil, false, nil, ErrMalformedExtension
	}
	return number, isDelta, baseRef, nil
}

// decodeASN1Integer 解析单个 DER INTEGER（tag 0x02）为任意精度整数，
// 避免把可能超过 int64 的 cRLNumber/baseCRLNumber 截断。
func decodeASN1Integer(der []byte) (*big.Int, error) {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(der, &raw)
	if err != nil || len(rest) != 0 {
		return nil, ErrMalformedExtension
	}
	if raw.Tag != 2 || raw.Class != asn1.ClassUniversal || raw.IsCompound {
		return nil, ErrMalformedExtension
	}
	if len(raw.Bytes) == 0 {
		return nil, ErrMalformedExtension
	}
	// DER INTEGER 使用二进制补码：最高位为 1 表示负数。
	n := new(big.Int).SetBytes(raw.Bytes)
	if raw.Bytes[0]&0x80 != 0 {
		nbits := uint(len(raw.Bytes) * 8)
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), nbits))
	}
	return n, nil
}

// parseScope 解析 IDP 扩展；无 IDP 时返回 present=false 的空范围。
func parseScope(rl *x509.RevocationList) (scope, error) {
	for _, ext := range rl.Extensions {
		if !ext.Id.Equal(oidExtIssuingDP) {
			continue
		}
		var idp asn1IDP
		if rest, err := asn1.Unmarshal(ext.Value, &idp); err != nil || len(rest) != 0 {
			return scope{}, ErrMalformedExtension
		}
		sc := scope{
			present:         true,
			onlyUserCerts:   idp.OnlyUserCerts,
			onlyCACerts:     idp.OnlyCACerts,
			onlySomeReasons: idp.OnlySomeReasons,
			indirectCRL:     idp.IndirectCRL,
		}
		if len(idp.DistributionPoint.FullName) > 0 {
			seq, err := asn1.Marshal(idp.DistributionPoint.FullName)
			if err != nil {
				return scope{}, ErrMalformedExtension
			}
			sc.fullName = seq
		} else {
			// nameRelativeToCRLIssuer 在本组件支持的单一分发点模型中不使用。
			sc.relative = true
		}
		return sc, nil
	}
	return scope{}, nil
}

func sameScope(a, b scope) bool {
	if a.present != b.present {
		return false
	}
	if !a.present {
		return true
	}
	return bytesEqual(a.fullName, b.fullName) &&
		a.relative == b.relative &&
		a.onlyUserCerts == b.onlyUserCerts &&
		a.onlyCACerts == b.onlyCACerts &&
		bitStringEqual(a.onlySomeReasons, b.onlySomeReasons) &&
		a.indirectCRL == b.indirectCRL
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bitStringEqual(a, b asn1.BitString) bool {
	if a.BitLength != b.BitLength || len(a.Bytes) != len(b.Bytes) {
		return false
	}
	return bytesEqual(a.Bytes, b.Bytes)
}

// insertSorted 按 cRLNumber 升序插入；同序号视为重放直接丢弃（返回原切片）。
func insertSorted(list []*crlHolder, h *crlHolder) []*crlHolder {
	for i, existing := range list {
		cmp := h.rl.Number.Cmp(existing.rl.Number)
		if cmp == 0 {
			return list
		}
		if cmp < 0 {
			list = append(list, nil)
			copy(list[i+1:], list[i:])
			list[i] = h
			return list
		}
	}
	return append(list, h)
}

// ingestBaseLocked 接入基础 CRL，并尝试激活之前暂存的增量。
func (c *Checker) ingestBaseLocked(h *crlHolder, number *big.Int) error {
	if c.base != nil && number.Cmp(c.base.rl.Number) <= 0 {
		return ErrStaleCRL
	}
	c.base = h

	// 新基础可能已包含旧增量中的吊销：丢弃被新基础序号覆盖的旧增量，
	// 其余增量按 §5.2.4 重新校验后保留。
	kept := c.deltas[:0]
	for _, d := range c.deltas {
		if d.rl.Number.Cmp(number) > 0 && c.validateDeltaWithLocked(d, d.rl.Number, deltaBaseRef(d.rl)) == nil {
			kept = append(kept, d)
		}
	}
	c.deltas = kept

	// 基础到位后，尝试按序激活暂存的增量。
	stillPending := c.pending[:0]
	for _, d := range c.pending {
		if err := c.ingestDeltaLocked(d, d.rl.Number, deltaBaseRef(d.rl)); err != nil {
			if errors.Is(err, ErrNoBaseCRL) || errors.Is(err, ErrFutureBaseReference) {
				stillPending = append(stillPending, d)
			}
		}
	}
	c.pending = stillPending

	return c.rebuildLocked()
}

// deltaBaseRef 从已解析的增量 CRL 重新取出 baseCRLNumber（暂存激活时使用）。
func deltaBaseRef(rl *x509.RevocationList) *big.Int {
	for _, ext := range rl.Extensions {
		if ext.Id.Equal(oidExtDeltaCRLIndicator) {
			if n, err := decodeASN1Integer(ext.Value); err == nil {
				return n
			}
		}
	}
	return nil
}

// ingestDeltaLocked 接入增量 CRL：校验范围与基础引用，通过后并入增量序列。
func (c *Checker) ingestDeltaLocked(h *crlHolder, number, baseRef *big.Int) error {
	if c.base == nil {
		c.pending = insertSorted(c.pending, h)
		return ErrNoBaseCRL
	}
	if err := c.validateDeltaWithLocked(h, number, baseRef); err != nil {
		return err
	}
	previous := c.deltas
	c.deltas = insertSorted(c.deltas, h)
	if err := c.rebuildLocked(); err != nil {
		c.deltas = previous // 合并失败：拒绝该增量并恢复上一份可用视图
		return err
	}
	return nil
}

// validateDeltaWithLocked 实现增量 CRL 的强制校验：
//   - 签发者必须与基础 CRL 一致（IngestDER 已对签名和 RawIssuer 校验，
//     这里再对合并视图做防御性确认）；
//   - 分发点范围（IDP）必须与基础 CRL 完全一致；
//   - baseCRLNumber 不能晚于当前基础 CRL 的 cRLNumber；
//   - 增量自身 cRLNumber 必须新于当前基础，且不与已接受的增量重号。
func (c *Checker) validateDeltaWithLocked(h *crlHolder, number, baseRef *big.Int) error {
	if !bytesEqual(h.rl.RawIssuer, c.base.rl.RawIssuer) {
		return ErrIssuerMismatch
	}
	if !sameScope(h.scope, c.base.scope) {
		return ErrScopeMismatch
	}
	if baseRef != nil && baseRef.Cmp(c.base.rl.Number) > 0 {
		return ErrFutureBaseReference
	}
	if number.Cmp(c.base.rl.Number) <= 0 {
		return ErrStaleCRL
	}
	for _, d := range c.deltas {
		if number.Cmp(d.rl.Number) == 0 {
			return ErrStaleCRL
		}
	}
	return nil
}

// rebuildLocked 按 RFC 5280 §5.3.3 重新合并基础 CRL 与全部增量 CRL。
// 合并失败（例如非法的 removeFromCRL）时保留上一份可用视图并返回错误。
func (c *Checker) rebuildLocked() error {
	if c.base == nil {
		c.merged = make(map[string]revokedState)
		return nil
	}

	merged := make(map[string]revokedState)
	applyEntries := func(rl *x509.RevocationList) bool {
		for _, entry := range rl.RevokedCertificateEntries {
			key := entry.SerialNumber.Text(16)
			if entry.ReasonCode == ReasonRemoveFromCRL {
				state, ok := merged[key]
				if ok && state.reason != ReasonCertificateHold {
					// 目标存在且并非 certificateHold：removeFromCRL 在 RFC 5280
					// §5.3.3 下非法，整份增量必须拒绝。
					return false
				}
				if !ok {
					// 目标尚不在已合并视图中：其暂停所在的增量可能仍在网络上
					// 乱序传输。先忽略该操作，后续增量触发 rebuild 时重新计算。
					continue
				}
				delete(merged, key)
				continue
			}
			merged[key] = revokedState{reason: entry.ReasonCode, at: entry.RevocationTime}
		}
		return true
	}

	if !applyEntries(c.base.rl) {
		return ErrInvalidRemoveFromCRL
	}
	// 增量按 cRLNumber 升序合并，与接入顺序无关，保证乱序到达结果一致。
	for _, d := range c.deltas {
		if !applyEntries(d.rl) {
			return ErrInvalidRemoveFromCRL
		}
	}
	c.merged = merged
	return nil
}

// expired 判断 CRL 是否已过 nextUpdate。按 RFC 5280，nextUpdate 是可选的；
// 缺失时无法判定过期，交由调用方的刷新策略兜底，这里视为未过期。
func expired(rl *x509.RevocationList, now time.Time) bool {
	if rl.NextUpdate.IsZero() {
		return false
	}
	return now.After(rl.NextUpdate)
}

// Check 返回证书 serial 在当前基础 CRL + 增量 CRL 视图下的吊销状态。
// 只要基础 CRL 或任一仍参与合并的增量 CRL 过期，一律返回 Unknown，
// 避免在可能已发生新吊销的情况下给出错误的 Good 结论。
func (c *Checker) Check(serial *big.Int) Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.base == nil {
		return Result{Status: Unknown}
	}
	now := c.now()
	if expired(c.base.rl, now) {
		return Result{Status: Unknown}
	}
	for _, d := range c.deltas {
		if expired(d.rl, now) {
			return Result{Status: Unknown}
		}
	}

	state, ok := c.merged[serial.Text(16)]
	if !ok {
		return Result{Status: Good}
	}
	return Result{Status: Revoked, Reason: state.reason, RevokedAt: state.at}
}

// BaseCRLNumber 返回当前基础 CRL 的序号；尚无基础 CRL 时返回 nil。
func (c *Checker) BaseCRLNumber() *big.Int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base == nil {
		return nil
	}
	return new(big.Int).Set(c.base.rl.Number)
}

// DeltaCount 返回当前已接受并参与合并的增量 CRL 数量。
func (c *Checker) DeltaCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.deltas)
}

// PendingCount 返回因基础 CRL 未到位而暂存的增量 CRL 数量。
func (c *Checker) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}
