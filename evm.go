package netsec

import "fmt"

// FileMeta 汇总评估 security.evm 所需的全部安全相关元数据。
//
// 调用方（LSM 钩子或完整性守护进程）在文件访问前通过 getxattr 与 stat
// 采集这些字段；本包只做纯元数据评估，不直接触碰文件系统。
type FileMeta struct {
	Path       string
	Ino        uint64 // stat.st_ino
	Generation uint32 // i_generation（stat.st_gen）
	UID        uint32
	GID        uint32
	Mode       uint32
	IMA        []byte // security.ima 扩展属性值
	SELinux    []byte // security.selinux 扩展属性值
	EVMAttr    []byte // security.evm 扩展属性值（首字节为类型，其后为 HMAC 或签名）

	// EVMBoundIno/EVMBoundGeneration 是 security.evm 被签发时绑定的 inode
	// 与 generation，由完整性引擎随扩展属性一起记录（例如安全清单/IMA 运行时
	// 状态）。两者与当前 stat 值不同即意味着文件已被复制或替换到新 inode。
	// 留零表示引擎没有签发期 inode 记录，此时只做全量 HMAC 比对，
	// 无法区分“属性篡改”与“跨 inode 复制”，统一按属性不匹配拒绝。
	EVMBoundIno        uint64
	EVMBoundGeneration uint32
}

// Status 是访问前评估结论。
type Status uint8

const (
	StatusDeny Status = iota
	StatusPass
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "pass"
	default:
		return "deny"
	}
}

// Reason 给出结论的具体成因，便于审计与告警分类。
type Reason uint8

const (
	ReasonNone           Reason = iota
	ReasonNoXattr               // 缺少 security.evm 扩展属性
	ReasonMalformed             // security.evm 载荷长度非法
	ReasonUnknownType           // 类型字节既不是 2 也不是 5
	ReasonNoKey                 // 评估所需密钥未加载
	ReasonAttrMismatch          // 受保护属性（ima/selinux/uid/gid/mode）被篡改
	ReasonInodeMismatch         // 属性一致但 inode/generation 不一致，HMAC 系从其他 inode 拷来
	ReasonBadSignature          // 可移植签名验证失败
	ReasonHMACValid             // 本机 HMAC 重算一致
	ReasonSignatureValid        // 可移植签名验证通过
)

func (r Reason) String() string {
	switch r {
	case ReasonNoXattr:
		return "no-evm-xattr"
	case ReasonMalformed:
		return "malformed-evm-attr"
	case ReasonUnknownType:
		return "unknown-evm-type"
	case ReasonNoKey:
		return "key-not-loaded"
	case ReasonAttrMismatch:
		return "protected-attr-mismatch"
	case ReasonInodeMismatch:
		return "inode-mismatch"
	case ReasonBadSignature:
		return "bad-signature"
	case ReasonHMACValid:
		return "hmac-valid"
	case ReasonSignatureValid:
		return "signature-valid"
	default:
		return "none"
	}
}

// Result 是一次 security.evm 评估的结论。
type Result struct {
	Status Status
	Reason Reason
	Path   string
	Detail string
}

// OK 报告是否允许访问。
func (r Result) OK() bool { return r.Status == StatusPass }

func (r Result) String() string {
	if r.Detail != "" {
		return fmt.Sprintf("evm %s path=%q reason=%s detail=%q",
			r.Status, r.Path, r.Reason, r.Detail)
	}
	return fmt.Sprintf("evm %s path=%q reason=%s", r.Status, r.Path, r.Reason)
}

// Engine 持有 EVM 评估所需的受信密钥材料，并发只读使用。
//
// HostKey 为类型字节 2 的本机 HMAC 主机密钥；TrustedKey 为类型字节 5
// 的可移植签名受信公钥。字段应在发布给多个 goroutine 之前完成配置。
type Engine struct {
	HostKey    []byte
	TrustedKey PublicKey
}

// KeysLoaded 报告是否加载了任意一类受信密钥材料。
func (e Engine) KeysLoaded() bool {
	return len(e.HostKey) > 0 || e.TrustedKey != nil
}

// Evaluate 在文件访问前评估 security.evm，任何不确定情形一律拒绝。
//
// 策略要点：
//   - 类型字节 2（本机 HMAC）：用主机密钥按固定顺序重算 HMAC 并常量时间比对；
//     不一致时进一步区分“属性被篡改”与“inode/generation 变了”。
//   - 类型字节 5（可移植签名）：不计 inode/generation，用受信公钥验证。
//   - 所需密钥未加载时一律拒绝，绝不退回只检查 security.ima。
//   - 未知类型字节、畸形载荷、缺失扩展属性一律拒绝。
func (e Engine) Evaluate(meta FileMeta) Result {
	res := Result{Path: meta.Path}
	deny := func(reason Reason, detail string) Result {
		res.Status, res.Reason, res.Detail = StatusDeny, reason, detail
		return res
	}
	pass := func(reason Reason, detail string) Result {
		res.Status, res.Reason, res.Detail = StatusPass, reason, detail
		return res
	}

	if len(meta.EVMAttr) == 0 {
		return deny(ReasonNoXattr, "security.evm xattr is absent")
	}

	switch meta.EVMAttr[0] {
	case TypeHMAC:
		// 密钥未加载：一律拒绝，不允许退回 IMA-only 检查。
		if len(e.HostKey) == 0 {
			return deny(ReasonNoKey,
				"EVM host HMAC key not loaded; IMA-only fallback is not permitted")
		}
		if len(meta.EVMAttr) != 1+hmacDigestSize {
			return deny(ReasonMalformed, fmt.Sprintf(
				"type-%d HMAC attr must be %d bytes, got %d",
				TypeHMAC, 1+hmacDigestSize, len(meta.EVMAttr)))
		}

		stored := meta.EVMAttr[1:]
		input, _ := canonicalInput(meta, TypeHMAC, true)
		full := evmHMAC(e.HostKey, input)
		if hmacEqual(stored, full) {
			return pass(ReasonHMACValid, "recomputed HMAC matches security.evm")
		}

		// 全量不匹配。若引擎持有签发期 inode 记录，则用“当前安全属性 +
		// 签发期 inode/generation”重算，精确区分两类攻击：
		//   - 命中：属性完好，文件连同一个合法 HMAC 被复制到了新 inode；
		//   - 不命中：至少一个受保护属性被离线篡改。
		// 没有签发期记录时无法区分，保守按属性篡改拒绝。
		if meta.EVMBoundIno != 0 {
			bound := meta
			bound.Ino, bound.Generation = meta.EVMBoundIno, meta.EVMBoundGeneration
			boundInput, _ := canonicalInput(bound, TypeHMAC, true)
			if hmacEqual(stored, evmHMAC(e.HostKey, boundInput)) {
				return deny(ReasonInodeMismatch,
					"protected attrs match but inode/generation differ; security.evm was copied from another inode")
			}
		}
		return deny(ReasonAttrMismatch,
			"protected attribute (security.ima/security.selinux/uid/gid/mode) mismatch")

	case TypePortableSignature:
		// 签名验证同样要求受信公钥已加载，不允许退回 IMA-only 检查。
		if e.TrustedKey == nil {
			return deny(ReasonNoKey,
				"trusted public key not loaded; IMA-only fallback is not permitted")
		}
		sig := meta.EVMAttr[1:]
		if len(sig) == 0 {
			return deny(ReasonMalformed, "type-5 portable signature has empty signature body")
		}
		message, _ := canonicalInput(meta, TypePortableSignature, false)
		if !e.TrustedKey.Verify(message, sig) {
			return deny(ReasonBadSignature,
				"portable signature failed verification against trusted public key")
		}
		return pass(ReasonSignatureValid,
			"portable signature verified; inode/generation are not covered by design")

	default:
		return deny(ReasonUnknownType, fmt.Sprintf(
			"unknown security.evm type byte %d (supported: %d HMAC, %d portable signature)",
			meta.EVMAttr[0], TypeHMAC, TypePortableSignature))
	}
}
