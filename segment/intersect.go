package segment

// Intersect 求两条倒排链的交集（AND 语义）。
// 以较短的链为驱动链顺序前进，长链通过 Advance 借助跳跃指针
// 整段跳过不可能命中的区间，避免逐条解码长链。
func Intersect(a, b *Iterator) []uint32 {
	if a == nil || b == nil {
		return nil
	}
	if a.Len() > b.Len() {
		a, b = b, a
	}
	var out []uint32
	okA := a.Next()
	okB := b.Next()
	for okA && okB {
		switch {
		case a.Doc() == b.Doc():
			out = append(out, a.Doc())
			okA = a.Next()
			okB = b.Next()
		case a.Doc() < b.Doc():
			okA = a.Advance(b.Doc())
		default:
			okB = b.Advance(a.Doc())
		}
	}
	return out
}
