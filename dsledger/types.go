package dsledger

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

// MemberID 是成员清单内成员的唯一标识。
type MemberID string

// Digest 是区块摘要（父摘要/区块摘要）的固定长度表示。
type Digest [32]byte

// ZeroDigest 是全零摘要，可作为显式的空摘要。
var ZeroDigest Digest

// Hex 返回小写十六进制编码。
func (d Digest) Hex() string {
	return hex.EncodeToString(d[:])
}

// ParseDigest 解析 64 位十六进制摘要。
func ParseDigest(s string) (Digest, error) {
	var out Digest
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("dsledger: parse digest: %w", err)
	}
	if len(raw) != len(out) {
		return out, fmt.Errorf("dsledger: digest length = %d, want %d", len(raw), len(out))
	}
	copy(out[:], raw)
	return out, nil
}

// MarshalText 实现 encoding.TextMarshaler。
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.Hex()), nil
}

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Phase 是份额消息所属的共识阶段。
type Phase uint8

const (
	// PhasePrepare 对应 prepare 阶段。
	PhasePrepare Phase = 1
	// PhaseCommit 对应 commit 阶段。
	PhaseCommit Phase = 2
)

// String 返回阶段的可读名称。
func (p Phase) String() string {
	switch p {
	case PhasePrepare:
		return "prepare"
	case PhaseCommit:
		return "commit"
	default:
		return fmt.Sprintf("phase(%d)", uint8(p))
	}
}

// Valid 报告阶段是否为受支持的取值。
func (p Phase) Valid() bool {
	return p == PhasePrepare || p == PhaseCommit
}

// MarshalText 使 JSON 持久化使用可读的阶段名。
func (p Phase) MarshalText() ([]byte, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("dsledger: invalid phase %d", uint8(p))
	}
	return []byte(p.String()), nil
}

// UnmarshalText 解析 "prepare"/"commit"。
func (p *Phase) UnmarshalText(text []byte) error {
	switch string(text) {
	case "prepare":
		*p = PhasePrepare
	case "commit":
		*p = PhaseCommit
	default:
		return fmt.Errorf("dsledger: invalid phase %q", string(text))
	}
	return nil
}

// Member 描述固定成员清单中的一个成员。
type Member struct {
	// ID 是成员在清单内的唯一 ID。
	ID MemberID
	// PublicKey 是该成员的 Ed25519 公钥。
	PublicKey ed25519.PublicKey
}

// Roster 是不可变的固定成员清单。
type Roster struct {
	chainID       string
	genesisDigest Digest
	order         []MemberID
	byID          map[MemberID]Member
}

// NewRoster 构造并校验成员清单：ID 唯一、公钥长度合法。
func NewRoster(chainID string, genesisDigest Digest, members []Member) (*Roster, error) {
	if chainID == "" {
		return nil, fmt.Errorf("dsledger: chain id must not be empty")
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("dsledger: roster must contain at least one member")
	}
	r := &Roster{
		chainID:       chainID,
		genesisDigest: genesisDigest,
		order:         make([]MemberID, 0, len(members)),
		byID:          make(map[MemberID]Member, len(members)),
	}
	for _, m := range members {
		if m.ID == "" {
			return nil, fmt.Errorf("dsledger: member id must not be empty")
		}
		if len(m.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("dsledger: member %s has invalid ed25519 public key", m.ID)
		}
		if _, exists := r.byID[m.ID]; exists {
			return nil, fmt.Errorf("dsledger: duplicate member id %q", m.ID)
		}
		r.byID[m.ID] = m
		r.order = append(r.order, m.ID)
	}
	return r, nil
}

// ChainID 返回清单绑定的链 ID。
func (r *Roster) ChainID() string { return r.chainID }

// GenesisDigest 返回高度 0 的创世（空父）摘要。
func (r *Roster) GenesisDigest() Digest { return r.genesisDigest }

// Member 按 ID 查询成员。
func (r *Roster) Member(id MemberID) (Member, bool) {
	m, ok := r.byID[id]
	return m, ok
}

// IDs 按清单顺序返回全部成员 ID。
func (r *Roster) IDs() []MemberID {
	out := make([]MemberID, len(r.order))
	copy(out, r.order)
	return out
}

// Share 是一条带 Ed25519 签名的阈值签名份额消息。
// 签名覆盖 ChainID、Height、Round、Phase、Block、Parent 六个字段，
// Signer 与 Sig 不进入被签名载荷（Sig 是对其余字段规范编码的签名）。
type Share struct {
	ChainID string   `json:"chain_id"`
	Signer  MemberID `json:"signer"`
	Height  uint64   `json:"height"`
	Round   uint64   `json:"round"`
	Phase   Phase    `json:"phase"`
	Block   Digest   `json:"block"`
	Parent  Digest   `json:"parent"`
	Sig     []byte   `json:"sig"`
}

// Key 唯一标识“某成员的某高度某阶段”的槽位。
type slotKey struct {
	Height uint64
	Phase  Phase
}

func (s Share) slot() slotKey {
	return slotKey{Height: s.Height, Phase: s.Phase}
}
