package dictneg

// Codec 把字典摘要绑定到具体的压缩实现。本包不内置任何压缩算法，
// 宿主用真实的字典压缩库实现该接口；测试中可用 NopCodec 或带记录的包装。
//
// 实现必须保证：同一摘要（即同一字典内容）构造出的 Decompressor 行为确定，
// 且 Open 对未知摘要返回 ErrUnknownDictionary 而不是静默回退。
type Codec interface {
	// Open 用给定字典内容构造解压器。content 为 nil（零摘要）时构造
	// 不依赖字典的基础解压器。
	Open(content []byte) (Decompressor, error)
}

// Decompressor 解压单个数据帧的载荷。
type Decompressor interface {
	// Decompress 解压一帧。实现不得跨帧借用其他字典的状态。
	Decompress(payload []byte) ([]byte, error)
}

// NopCodec 是不做任何压缩的 Codec：解压结果即载荷本身。
// 用于无字典引导阶段，也方便宿主在未接入压缩库时先打通协商链路。
type NopCodec struct{}

// Open 实现 Codec。
func (NopCodec) Open([]byte) (Decompressor, error) {
	return nopDecompressor{}, nil
}

type nopDecompressor struct{}

func (nopDecompressor) Decompress(payload []byte) ([]byte, error) {
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}
