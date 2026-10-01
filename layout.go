package sparse

import "fmt"

// LayoutString 返回持久结构编码布局与逐部分字节明细（用于演示与审计）。
func (a *Array) LayoutString() string {
	s := fmt.Sprintf("逻辑长度 n=%d  缺省值 def=%d  稠密等效 %d B\n", a.n, a.v.def, a.LogicalBytes())
	s += fmt.Sprintf("头部(n,def)                                    %4d B\n", 16)
	s += fmt.Sprintf("惰性层 segs            %4d 段 x 24 B           %4d B\n",
		len(a.v.segs), len(a.v.segs)*24)
	if r := a.v.run; r != nil {
		s += fmt.Sprintf("例外索引 run: 目录 bases   %4d 块 x 8 B           %4d B\n",
			len(r.bases), len(r.bases)*8)
		for i := range r.blocks {
			b := &r.blocks[i]
			s += fmt.Sprintf("  块%d base=%d n=%d: 头10B + posEnc %dB + valEnc %dB = %d B\n",
				i, b.base, b.n, len(b.posEnc), len(b.valEnc), 10+len(b.posEnc)+len(b.valEnc))
		}
	} else {
		s += "例外索引 run:            空                        0 B\n"
	}
	s += fmt.Sprintf("写缓冲 buf             %4d 条 x 16 B           %4d B\n",
		len(a.v.bufPos), len(a.v.bufPos)*16)
	s += fmt.Sprintf("物理占用合计                                   %4d B\n", a.PhysicalBytes())
	return s
}
