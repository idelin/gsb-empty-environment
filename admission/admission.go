package admission

import (
	"bytes"
	"errors"
)

// V2SchemeID 是 APK Signature Scheme v2 的 ID-pair 标识。
const V2SchemeID uint32 = 0x7109871a

// V3SchemeID 是 APK Signature Scheme v3 的 ID-pair 标识。
const V3SchemeID uint32 = 0xf05368c0

// MinSDKForV2 是强制要求 v2 及以上签名的最低系统版本（Android 7.0 / API 24）。
const MinSDKForV2 = 24

var (
	// dexMagic 是 DEX 文件开头的魔数 "dex\n"。
	dexMagic = []byte("dex\n")
	// zipLocalFileMagic 是 ZIP 本地文件头魔数 "PK\x03\x04" 的前 4 字节。
	zipLocalFileMagic = []byte{0x50, 0x4b, 0x03, 0x04}
)

// 准入失败原因。调用方可用 errors.Is 判定具体类别。
var (
	// ErrInvalidInput 表示输入结构本身不完整或字段自相矛盾。
	ErrInvalidInput = errors.New("admission: invalid input")
	// ErrPolymorphic 表示文件是 DEX/ZIP 多态文件（DEX 前缀，或头部不是
	// ZIP 本地文件头魔数却存在合法 EOCD），即 Janus 类拼接攻击特征。
	ErrPolymorphic = errors.New("admission: polymorphic dex/zip package rejected")
	// ErrLocalHeaderNotAtZero 表示第一个本地文件头不在偏移 0。
	ErrLocalHeaderNotAtZero = errors.New("admission: first local file header is not at offset 0")
	// ErrUnsupportedSignature 表示最低系统版本要求 v2 及以上签名，
	// 但包内没有 v2/v3 签名（只有 v1 或完全没有）。
	ErrUnsupportedSignature = errors.New("admission: minSdkVersion >= 24 requires APK signature scheme v2 or higher")
	// ErrSigBlockSizeMismatch 表示签名分块前后两个 uint64 长度字段不一致。
	ErrSigBlockSizeMismatch = errors.New("admission: APK signing block size fields mismatch")
	// ErrSigBlockNotAdjacent 表示签名分块之后没有紧跟中央目录。
	ErrSigBlockNotAdjacent = errors.New("admission: APK signing block is not immediately followed by central directory")
	// ErrDigestCoverage 表示回调确认摘要覆盖范围不满足三段要求或缺段。
	ErrDigestCoverage = errors.New("admission: v2/v3 digest does not cover all required ranges")
	// ErrVerifierMissing 表示存在 v2/v3 签名但调用方没有提供摘要校验回调。
	ErrVerifierMissing = errors.New("admission: v2/v3 signature present but no digest verifier callback provided")
)

// Range 是文件内的一个连续字节区间 [Start, End)。
type Range struct {
	Start int64 // 区间起始偏移（含）
	End   int64 // 区间结束偏移（不含）
}

// SigBlock 描述 APK Signing Block 的解析结果。
type SigBlock struct {
	Present           bool   // 是否存在签名分块（以 "APK Sig Block 42" 魔数结尾）
	Start             int64  // 分块首字节（开头 sizeOfBlock 字段）的偏移
	SizeOfBlockBefore uint64 // 分块开头字段：head(8)+pairs+tail(8)，不含 magic
	SizeOfBlockAfter  uint64 // 分块尾部字段：pairs+tail(8)+magic(16)，不含 head
	HasV2             bool   // 是否存在 v2 签名 ID-pair（0x7109871a）
	HasV3             bool   // 是否存在 v3 签名 ID-pair（0xf05368c0）
}

// HasModernSignature 报告是否携带 v2 或 v3 签名。
func (s SigBlock) HasModernSignature() bool {
	return s.HasV2 || s.HasV3
}

// DigestCoverage 是 v2/v3 摘要必须覆盖的三段内容：偏移 0 到签名分块
// 之前的内容、中央目录、结束记录（EOCD）。
type DigestCoverage struct {
	BeforeSigBlock   Range // [0, 签名分块起始偏移)
	CentralDirectory Range // 中央目录区间
	EOCD             Range // 结束记录区间
}

// DigestVerifier 由调用方实现，负责 v2/v3 摘要与签名的密码学校验，
// 并确认实际被摘要保护的字节区间与 coverage 完全一致、三段齐全。
// 返回 nil 表示覆盖范围正确且签名校验通过；返回非 nil（通常包装
// ErrDigestCoverage）表示覆盖缺段、范围不符或签名无效，准入将被拒绝。
type DigestVerifier func(coverage DigestCoverage, block SigBlock) error

// Input 是准入判定所需的安装包结构化信息。偏移均为相对文件起始的字节偏移。
type Input struct {
	First8                 []byte         // 文件前 8 字节，长度必须为 8
	CentralDirectory       Range          // 中央目录区间 [Start, End)
	EOCD                   Range          // 结束记录区间 [Start, End)
	FirstLocalHeaderOffset int64          // 第一个本地文件头的偏移
	SigBlock               SigBlock       // APK 签名分块信息
	MinSDKVersion          int            // 目标最低系统版本（API Level）
	VerifyDigest           DigestVerifier // v2/v3 摘要范围与签名校验回调
}

// Admit 对结构化输入执行安装包准入判定，返回 nil 表示放行。
func Admit(in Input) error {
	if len(in.First8) < 8 {
		return errors.Join(ErrInvalidInput, errors.New("first 8 bytes are required"))
	}
	if err := validateRanges(in); err != nil {
		return err
	}

	// 多态文件判定：DEX 魔数开头，或头部不是 ZIP 本地文件头魔数却存在
	// 合法 EOCD（前置字节会被 ZIP 解析器忽略，却可能被系统当作 DEX 加载）。
	if bytes.HasPrefix(in.First8, dexMagic) {
		return ErrPolymorphic
	}

	// 第一个本地文件头必须位于偏移 0，杜绝任何前缀拼接。
	if in.FirstLocalHeaderOffset != 0 {
		return ErrLocalHeaderNotAtZero
	}

	// 头部不是 ZIP 本地文件头魔数却存在合法 EOCD：前置字节会被 ZIP 解析器
	// 忽略，却可能被系统当作其它格式加载，判为多态文件。
	hasValidEOCD := in.EOCD.Start >= 0 && in.EOCD.End > in.EOCD.Start
	if !bytes.HasPrefix(in.First8, zipLocalFileMagic) && hasValidEOCD {
		return ErrPolymorphic
	}

	// 存在签名分块时，校验前后长度字段一致，且分块之后紧跟中央目录。
	if in.SigBlock.Present {
		// after 用 magic(16) 替换了 head(8)，合法时 after = before + 8。
		if in.SigBlock.SizeOfBlockAfter != in.SigBlock.SizeOfBlockBefore+8 {
			return ErrSigBlockSizeMismatch
		}
		// 分块物理总长 = after + head(8)（after 已含 magic），
		// 其结束位置必须正好等于中央目录起点，即分块之后紧跟中央目录。
		blockEnd := in.SigBlock.Start + int64(in.SigBlock.SizeOfBlockAfter) + 8
		if in.SigBlock.Start < 0 || blockEnd != in.CentralDirectory.Start {
			return ErrSigBlockNotAdjacent
		}
	}

	// 版本闸门：minSdkVersion >= 24 必须有 v2 及以上签名；
	// 仅 v1（或无签名）只在 minSdkVersion < 24 时可接受。
	if in.MinSDKVersion >= MinSDKForV2 && !in.SigBlock.HasModernSignature() {
		return ErrUnsupportedSignature
	}

	// 携带 v2/v3 签名时，回调必须确认摘要覆盖三段且范围精确。
	if in.SigBlock.HasModernSignature() {
		if in.VerifyDigest == nil {
			return ErrVerifierMissing
		}
		coverage := DigestCoverage{
			BeforeSigBlock: Range{Start: 0, End: in.SigBlock.Start},
			CentralDirectory: Range{
				Start: in.CentralDirectory.Start,
				End:   in.CentralDirectory.End,
			},
			EOCD: Range{Start: in.EOCD.Start, End: in.EOCD.End},
		}
		if err := in.VerifyDigest(coverage, in.SigBlock); err != nil {
			return errors.Join(ErrDigestCoverage, err)
		}
	}

	return nil
}

// validateRanges 检查各偏移区间非负、有序且相互衔接，杜绝伪造 EOCD
// 注释长度等手段造成的偏移溢出/回绕。
func validateRanges(in Input) error {
	if in.CentralDirectory.Start < 0 || in.CentralDirectory.End <= in.CentralDirectory.Start {
		return errors.Join(ErrInvalidInput, errors.New("invalid central directory range"))
	}
	if in.EOCD.Start < 0 || in.EOCD.End <= in.EOCD.Start {
		return errors.Join(ErrInvalidInput, errors.New("invalid EOCD range"))
	}
	if in.FirstLocalHeaderOffset < 0 {
		return errors.Join(ErrInvalidInput, errors.New("negative first local header offset"))
	}
	if in.SigBlock.Present {
		if in.SigBlock.Start < 0 {
			return errors.Join(ErrInvalidInput, errors.New("negative signing block offset"))
		}
		if in.SigBlock.Start > in.CentralDirectory.Start {
			return errors.Join(ErrInvalidInput, errors.New("signing block overlaps central directory"))
		}
	}
	if in.EOCD.Start != in.CentralDirectory.End {
		return errors.Join(ErrInvalidInput, errors.New("central directory is not immediately followed by EOCD"))
	}
	return nil
}
