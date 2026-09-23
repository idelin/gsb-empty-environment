package blockdict

import "crypto/sha256"

// Dict 是只读共享字典。编码端与解码端必须持有字节完全一致的字典，
// 块头中绑定其 SHA-256 摘要，摘要不匹配即拒绝解码。
type Dict struct {
	data   []byte
	digest [sha256.Size]byte
}

// NewDict 构造字典。data 可以为 nil（空字典），此时仍可正常编码，
// 只是压缩只能利用块内重复。构造后 data 不得再修改。
func NewDict(data []byte) *Dict {
	d := &Dict{data: data}
	d.digest = sha256.Sum256(data)
	return d
}

// Digest 返回字典内容的 SHA-256 摘要。
func (d *Dict) Digest() [sha256.Size]byte { return d.digest }

// Len 返回字典字节数。
func (d *Dict) Len() int { return len(d.data) }

func (d *Dict) bytes() []byte {
	if d == nil {
		return nil
	}
	return d.data
}

func (d *Dict) sum() [sha256.Size]byte {
	if d == nil {
		return sha256.Sum256(nil)
	}
	return d.digest
}
