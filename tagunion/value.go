package tagunion

// Kind 区分解码结果：已理解的分支 vs 原样保留的未知分支。
type Kind int

const (
	KindKnown   Kind = iota // 按已注册 schema 解码，并显式迁移到最新版本
	KindUnknown             // 未知标签/版本：不假装理解，原始片段透传
)

// Value 是一个解码后的标签联合对象。
type Value struct {
	Kind    Kind
	Tag     uint64
	Version uint64 // 未知分支同样保留原始版本号

	// KindKnown：按最新 schema 迁移后的字段集。
	Fields map[string]any

	// KindUnknown：原始载荷与完整原始帧（含版本与校验），
	// 重编码时逐字节透传，保证原字节不变。
	Raw   []byte
	Frame []byte
}
