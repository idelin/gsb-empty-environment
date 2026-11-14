package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// FeeMode 费用承担方式。
type FeeMode string

const (
	FeeSHA FeeMode = "SHA" // 双方共同承担
	FeeBEN FeeMode = "BEN" // 收款人承担
	FeeOUR FeeMode = "OUR" // 汇款人承担
)

// Instruction 汇款指令。端到端签名覆盖以下全部字段，
// 中转节点只允许转发，任何改写都会导致验签失败。
type Instruction struct {
	InstructionID   string  `json:"instruction_id"`
	Originator      string  `json:"originator"`
	OriginatorBank  string  `json:"originator_bank"`
	BeneficiaryBank string  `json:"beneficiary_bank"` // 收款行
	BeneficiaryAcct string  `json:"beneficiary_acct"` // 收款账号
	BeneficiaryName string  `json:"beneficiary_name"`
	Currency        string  `json:"currency"`
	AmountCents     int64   `json:"amount_cents"` // 金额（最小货币单位，避免浮点误差）
	FeeMode         FeeMode `json:"fee_mode"`     // 费用承担方式
	RemittanceInfo  string  `json:"remittance_info,omitempty"`
	ValueDate       string  `json:"value_date"` // YYYY-MM-DD
}

// HopRecord 每个经手节点在验签通过后追加的转发记录。
type HopRecord struct {
	Seq        int    `json:"seq"`
	NodeID     string `json:"node_id"`
	Action     string `json:"action"` // "ORIGINATE" | "FORWARD" | "DELIVER"
	MsgDigest  string `json:"msg_digest"`  // 该节点看到的报文摘要（hex）
	PrevDigest string `json:"prev_digest"` // 上一条记录的摘要，形成链
	Signature  []byte `json:"signature"`   // 节点对本记录的签名
}

// Message 在链路上传输的汇款报文。
type Message struct {
	Instruction Instruction `json:"instruction"`
	OriginKeyID string      `json:"origin_key_id"`
	Signature   []byte      `json:"signature"` // 发起方对指令规范编码的签名
	Hops        []HopRecord `json:"hops"`
}

// KeyRegistry 网关侧维护的公钥注册表：keyID -> 公钥。
type KeyRegistry struct {
	keys map[string]ed25519.PublicKey
}

func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{keys: make(map[string]ed25519.PublicKey)}
}

func (r *KeyRegistry) Register(keyID string, pub ed25519.PublicKey) {
	r.keys[keyID] = append(ed25519.PublicKey(nil), pub...)
}

func (r *KeyRegistry) Lookup(keyID string) (ed25519.PublicKey, bool) {
	p, ok := r.keys[keyID]
	return p, ok
}

// 已定义错误，供网关分类处理（退回/告警）。
var (
	ErrUnknownOriginKey = errors.New("netsec: unknown origin key id")
	ErrMissingSignature = errors.New("netsec: missing originator signature")
	ErrBadSignature     = errors.New("netsec: originator signature mismatch")
	ErrTamperedMessage  = errors.New("netsec: message content altered in transit")
	ErrBrokenHopChain   = errors.New("netsec: hop chain integrity failure")
	ErrUnknownHopKey    = errors.New("netsec: unknown hop node key id")
	ErrBadHopSignature  = errors.New("netsec: hop record signature mismatch")
)

// canonicalEncode 生成指令的确定性编码（定长整数、固定字段序），
// 作为签名输入，保证任意节点重算结果一致。
func canonicalEncode(in Instruction) []byte {
	var buf bytes.Buffer
	put := func(s string) {
		var ln [4]byte
		binary.BigEndian.PutUint32(ln[:], uint32(len(s)))
		buf.Write(ln[:])
		buf.WriteString(s)
	}
	put(in.InstructionID)
	put(in.Originator)
	put(in.OriginatorBank)
	put(in.BeneficiaryBank)
	put(in.BeneficiaryAcct)
	put(in.BeneficiaryName)
	put(in.Currency)
	var amt [8]byte
	binary.BigEndian.PutUint64(amt[:], uint64(in.AmountCents))
	buf.Write(amt[:])
	put(string(in.FeeMode))
	put(in.RemittanceInfo)
	put(in.ValueDate)
	return buf.Bytes()
}

// Digest 返回整条报文（指令+发起方签名）的摘要，用于逐跳记录。
func (m *Message) Digest() []byte {
	h := sha256.New()
	h.Write(canonicalEncode(m.Instruction))
	h.Write(m.Signature)
	return h.Sum(nil)
}

func digestHex(b []byte) string { return hex.EncodeToString(b) }

// hopRecordDigest 计算单条转发记录的摘要（不含其签名字段）。
func hopRecordDigest(h HopRecord) []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%s",
		h.Seq, h.NodeID, h.Action, h.MsgDigest, h.PrevDigest)))
	return sum[:]
}

// Sign 发起方对汇款指令签名，生成待发送报文。
func Sign(in Instruction, keyID string, priv ed25519.PrivateKey) (*Message, error) {
	if keyID == "" {
		return nil, errors.New("netsec: key id required")
	}
	sig := ed25519.Sign(priv, canonicalEncode(in))
	return &Message{
		Instruction: in,
		OriginKeyID: keyID,
		Signature:   sig,
	}, nil
}

// VerifyOrigin 校验发起方签名：覆盖收款行、账号、金额、费用承担方式等全部字段。
func VerifyOrigin(m *Message, reg *KeyRegistry) error {
	if len(m.Signature) == 0 {
		return ErrMissingSignature
	}
	pub, ok := reg.Lookup(m.OriginKeyID)
	if !ok {
		return ErrUnknownOriginKey
	}
	if !ed25519.Verify(pub, canonicalEncode(m.Instruction), m.Signature) {
		return ErrBadSignature
	}
	return nil
}

// ProcessHop 中转/接收节点处理入口：
//  1. 校验发起方端到端签名（防改写、防剥离）；
//  2. 校验既有跳链完整性（防记录伪造）；
//  3. 追加本节点转发记录并签名。
//
// 任一步失败即返回错误，网关应据此退回报文，不得继续转发或入账。
func ProcessHop(m *Message, reg *KeyRegistry, nodeID, action string, nodePriv ed25519.PrivateKey) error {
	if err := VerifyOrigin(m, reg); err != nil {
		return err
	}
	if err := verifyHopChain(m, reg); err != nil {
		return err
	}
	prev := ""
	if n := len(m.Hops); n > 0 {
		prev = digestHex(hopRecordDigest(m.Hops[n-1]))
	}
	rec := HopRecord{
		Seq:        len(m.Hops),
		NodeID:     nodeID,
		Action:     action,
		MsgDigest:  digestHex(m.Digest()),
		PrevDigest: prev,
	}
	rec.Signature = ed25519.Sign(nodePriv, hopRecordDigest(rec))
	m.Hops = append(m.Hops, rec)
	return nil
}

// verifyHopChain 校验已有转发记录：每跳摘要须与当前报文一致、
// 链式前向引用连续、节点签名有效。
func verifyHopChain(m *Message, reg *KeyRegistry) error {
	want := digestHex(m.Digest())
	prev := ""
	for i, h := range m.Hops {
		if h.Seq != i || h.PrevDigest != prev {
			return ErrBrokenHopChain
		}
		if h.MsgDigest != want {
			return ErrTamperedMessage
		}
		pub, ok := reg.Lookup(h.NodeID)
		if !ok {
			return ErrUnknownHopKey
		}
		if !ed25519.Verify(pub, hopRecordDigest(h), h.Signature) {
			return ErrBadHopSignature
		}
		prev = digestHex(hopRecordDigest(h))
	}
	return nil
}

// FinalVerify 收款行入账前的最终核验：端到端签名与全部跳记录。
// 通过后款项只能按指令中的原收款行/账号入账。
func FinalVerify(m *Message, reg *KeyRegistry) error {
	if err := VerifyOrigin(m, reg); err != nil {
		return err
	}
	return verifyHopChain(m, reg)
}

// HopTrail 返回可审计的转发轨迹（供网关日志/对账）。
func HopTrail(m *Message) []string {
	trail := make([]string, 0, len(m.Hops))
	for _, h := range m.Hops {
		trail = append(trail, fmt.Sprintf("#%d %s %s digest=%s", h.Seq, h.Action, h.NodeID, h.MsgDigest[:16]))
	}
	return trail
}

// Marshal / Unmarshal 供网关在线路上序列化报文。
func Marshal(m *Message) ([]byte, error) { return json.Marshal(m) }

func Unmarshal(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// sortedKeyIDs 仅用于测试与调试展示。
func (r *KeyRegistry) sortedKeyIDs() []string {
	ids := make([]string, 0, len(r.keys))
	for id := range r.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
