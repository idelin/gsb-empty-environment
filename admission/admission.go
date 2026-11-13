package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// 包 admission 为密钥服务器实现“更新包准入过滤”，抵御第三方认证签名泛滥
// 攻击（certificate flooding）。调用方把已解析的公钥包交给 Admit，由本包
// 按结构上限、签名者白名单和验证预算裁剪；真正的密码学验证通过 VerifyFunc
// 回调交给调用方完成。
//
// 规则：
//   - 每个用户标识只保留一条可验证的最新自签名；自签名无法验证时整个
//     用户标识丢弃。
//   - 第三方签名必须来自调用方白名单并通过回调验证，每个用户标识最多
//     保留 MaxThirdPartyPerUID(50) 条。
//   - 每个公钥最多 MaxUIDs(10) 个用户标识、MaxSubkeys(20) 个子密钥，
//     超出时按创建时间保留较新的；这只是裁剪，不拒绝整个公钥。
//   - 裁剪后的包序列化体积超过 MaxPacketSize(1 MiB) 时拒绝。
//   - 合并已存公钥时（Store.Merge），被丢弃的签名写入墓碑集合，后续更新
//     无法加回；签名以内容摘要（SHA-256）去重。

const (
	// MaxThirdPartyPerUID 是单个用户标识允许保留的第三方签名上限。
	MaxThirdPartyPerUID = 50
	// MaxUIDs 是单个公钥允许保留的用户标识上限。
	MaxUIDs = 10
	// MaxSubkeys 是单个公钥允许保留的子密钥上限。
	MaxSubkeys = 20
	// MaxPacketSize 是裁剪后公钥包允许的最大序列化体积（1 MiB）。
	MaxPacketSize = 1 << 20
)

var (
	// ErrPacketTooLarge 表示裁剪后的公钥包序列化体积超过 1 MiB。
	ErrPacketTooLarge = errors.New("admission: filtered public-key packet exceeds 1 MiB")
	// ErrFingerprintMismatch 表示合并时新旧包的主密钥指纹不一致。
	ErrFingerprintMismatch = errors.New("admission: primary key fingerprint mismatch")
)

// Signature 是已解析的签名（自签名或第三方认证签名）。
type Signature struct {
	// Raw 是签名包的规范字节，内容摘要和体积估算都基于它。
	Raw []byte
	// Issuer 是签名者标识（通常是主密钥指纹或长密钥 ID）。
	Issuer string
	// Created 是签名创建时间，用于挑选“最新”自签名及按时间裁剪。
	Created time.Time
	// Self 为 true 表示这是用户标识上的自签名。
	Self bool
}

// UserID 是一个用户标识及其上的自签名与第三方签名。
type UserID struct {
	// Raw 是用户标识包的规范字节。
	Raw []byte
	// Created 是该用户标识的创建时间（通常取自其主自签名）。
	Created time.Time
	// Signatures 是该用户标识上的全部签名（自签名 + 第三方签名）。
	Signatures []Signature
}

// Subkey 是已解析的子密钥。
type Subkey struct {
	// Raw 是子密钥包（含绑定签名）的规范字节。
	Raw []byte
	// Created 是子密钥创建时间，用于按时间保留较新的子密钥。
	Created time.Time
}

// PublicKeyPacket 是已经解析好的公钥更新包，是过滤器的输入。
type PublicKeyPacket struct {
	// Fingerprint 是主密钥指纹，作为公钥的稳定身份与合并主键。
	Fingerprint string
	// PrimaryRaw 是主密钥包的规范字节。
	PrimaryRaw []byte
	// UserIDs 是全部用户标识及其签名。
	UserIDs []UserID
	// Subkeys 是全部子密钥。
	Subkeys []Subkey
}

// VerifyFunc 由调用方实现真正的密码学签名验证；返回 true 表示签名对该
// 用户标识有效。过滤器自身不做密码学运算，只负责调度验证次数（受预算
// 约束）与结构裁剪。
type VerifyFunc func(pk *PublicKeyPacket, uid UserID, sig Signature) bool

// SerializeFunc 返回公钥包的序列化字节，用于 1 MiB 体积上限判定。
// nil 时使用 DefaultSerializer，即对各组成部分的 Raw 做长度前缀拼接估算。
type SerializeFunc func(pk *PublicKeyPacket) ([]byte, error)

// Whitelist 是调用方认可的第三方签名者集合，键为签名者标识，与
// Signature.Issuer 直接比较。
type Whitelist map[string]struct{}

// NewWhitelist 用给定签名者标识构造白名单。
func NewWhitelist(issuers ...string) Whitelist {
	wl := make(Whitelist, len(issuers))
	for _, issuer := range issuers {
		wl[issuer] = struct{}{}
	}
	return wl
}

// Allowed 判断签名者是否在白名单内。nil/空白名单拒绝所有第三方签名。
func (wl Whitelist) Allowed(issuer string) bool {
	if len(wl) == 0 {
		return false
	}
	_, ok := wl[issuer]
	return ok
}

// Options 是一次准入过滤的调用方输入。
type Options struct {
	// Whitelist 是允许的第三方签名者。
	Whitelist Whitelist
	// Budget 限制本次准入实际执行的回调验证次数：
	//   - Budget > 0：最多验证 Budget 个签名；
	//   - Budget == 0：不做任何验证（未验证的自签名导致对应用户标识丢弃，
	//     未验证的第三方签名被丢弃但不写墓碑，允许后续预算充足时重试）；
	//   - Budget < 0：不限制验证次数。
	Budget int
	// Verify 是签名验证回调，Admit 时必须非 nil。
	Verify VerifyFunc
	// Serialize 用于体积上限判定，nil 时使用 DefaultSerializer。
	Serialize SerializeFunc
}

// Stats 记录一次准入过滤的计数，便于观测攻击面与裁剪结果。
type Stats struct {
	UIDsIn              int
	UIDsKept            int
	UIDsDropped         int
	SubkeysIn           int
	SubkeysKept         int
	SelfSigsVerified    int
	SelfSigsRejected    int
	ThirdPartyIn        int
	ThirdPartyVerified  int
	ThirdPartyRejected  int
	ThirdPartyDuplicate int
	ThirdPartyCapped    int
	ThirdPartyDeferred  int
	VerificationsUsed   int
	TombstonesAdded     int
}

// Result 是一次准入过滤的结果。
type Result struct {
	// Packet 是裁剪后的公钥包；被拒绝时为 nil。
	Packet *PublicKeyPacket
	// Tombstones 是本次涉及的被丢弃签名内容摘要集合（含输入已有墓碑）。
	Tombstones map[string]struct{}
	// Stats 是裁剪计数。
	Stats Stats
}

// Digest 计算签名的内容摘要（SHA-256，十六进制），用于去重与墓碑。
func Digest(sig Signature) string {
	sum := sha256.Sum256(sig.Raw)
	return hex.EncodeToString(sum[:])
}

// DefaultSerializer 以 4 字节长度前缀拼接各 Raw 部分估算序列化体积。
// 它不依赖任何外部 OpenPGP 库，保证体积上限始终可判定；接入真实服务时
// 可用 Options.Serialize 替换为严格的 wire 格式序列化。
func DefaultSerializer(pk *PublicKeyPacket) ([]byte, error) {
	if pk == nil {
		return nil, nil
	}
	chunks := 1 + len(pk.Subkeys)
	for i := range pk.UserIDs {
		chunks += 1 + len(pk.UserIDs[i].Signatures)
	}
	size := chunks*4 + len(pk.PrimaryRaw)
	for i := range pk.UserIDs {
		size += len(pk.UserIDs[i].Raw)
		for j := range pk.UserIDs[i].Signatures {
			size += len(pk.UserIDs[i].Signatures[j].Raw)
		}
	}
	for i := range pk.Subkeys {
		size += len(pk.Subkeys[i].Raw)
	}
	out := make([]byte, 0, size)
	out = appendLenPrefixed(out, pk.PrimaryRaw)
	for i := range pk.UserIDs {
		out = appendLenPrefixed(out, pk.UserIDs[i].Raw)
		for j := range pk.UserIDs[i].Signatures {
			out = appendLenPrefixed(out, pk.UserIDs[i].Signatures[j].Raw)
		}
	}
	for i := range pk.Subkeys {
		out = appendLenPrefixed(out, pk.Subkeys[i].Raw)
	}
	return out, nil
}

func appendLenPrefixed(dst, raw []byte) []byte {
	var prefix [4]byte
	prefix[0] = byte(len(raw) >> 24)
	prefix[1] = byte(len(raw) >> 16)
	prefix[2] = byte(len(raw) >> 8)
	prefix[3] = byte(len(raw))
	dst = append(dst, prefix[:]...)
	return append(dst, raw...)
}

func countThirdParty(sigs []Signature) int {
	n := 0
	for _, sig := range sigs {
		if !sig.Self {
			n++
		}
	}
	return n
}

// newerFirst 定义签名的“较新”顺序：创建时间降序，时间相同按摘要升序，
// 保证选择与裁剪是确定性的。
func newerFirst(a, b Signature) bool {
	if !a.Created.Equal(b.Created) {
		return a.Created.After(b.Created)
	}
	return Digest(a) < Digest(b)
}

func uidDigest(uid UserID) string {
	sum := sha256.Sum256(uid.Raw)
	return hex.EncodeToString(sum[:])
}
