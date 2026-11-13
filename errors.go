package netsec

import "errors"

var (
	// ErrInvalidName 表示明文名不是有效 UTF-8、为空、含斜杠或 NUL、或为点段。
	ErrInvalidName = errors.New("netsec: invalid plaintext name")
	// ErrNameTooLong 表示原始名或规范名超过目录策略的字节上限。
	ErrNameTooLong = errors.New("netsec: name exceeds policy byte limit")
	// ErrExist 表示同一规范名已存在且原始名完全相同。
	ErrExist = errors.New("netsec: entry already exists")
	// ErrCanonicalConflict 表示规范名与已有目录项相同但原始名不同。
	ErrCanonicalConflict = errors.New("netsec: canonical name conflicts with existing entry")
	// ErrNotFound 表示查找键桶中没有可解密匹配的目录项。
	ErrNotFound = errors.New("netsec: entry not found")
	// ErrIntegrity 表示候选目录项 AEAD 解密或 inode 身份核对失败。
	ErrIntegrity = errors.New("netsec: entry integrity check failed")
	// ErrInvalidPolicy 表示目录策略字段非法。
	ErrInvalidPolicy = errors.New("netsec: invalid directory policy")
	// ErrRotationActive 表示目录已处于密钥代次轮换中。
	ErrRotationActive = errors.New("netsec: key rotation already in progress")
	// ErrNoRotation 表示目录未处于轮换中。
	ErrNoRotation = errors.New("netsec: no key rotation in progress")
	// ErrRotationIncomplete 表示仍有旧代次目录项未迁移。
	ErrRotationIncomplete = errors.New("netsec: rotation incomplete, old-generation entries remain")
)
