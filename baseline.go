package cabac

// 本文件实现用于对比的无上下文基线模型，复用同一套二值化规则，
// 仅替换概率模型，从而公平地量化"上下文自适应"带来的收益。

// EncodeInstrsStatic 用完全静态的固定 0.5 概率模型编码（无上下文、无自适应）。
func EncodeInstrsStatic(instrs []Instr) []byte {
	enc := NewEncoder()
	for _, instr := range instrs {
		for _, b := range binarize(instr) {
			enc.EncodeBinBypass(b)
		}
	}
	enc.Flush()
	return enc.Bytes()
}

// EncodeInstrsSingleContext 让所有 bin 共享同一个自适应上下文：
// 有概率自适应，但不区分语法元素、bin 位置与邻接条件（无上下文建模）。
func EncodeInstrsSingleContext(instrs []Instr) []byte {
	enc := NewEncoder()
	var shared Context
	for _, instr := range instrs {
		for _, b := range binarize(instr) {
			enc.EncodeBin(&shared, b)
		}
	}
	enc.Flush()
	return enc.Bytes()
}

// FixedLengthBits 返回定长编码（3 比特操作码 + 4 比特操作数）的总比特数。
func FixedLengthBits(n int) int { return 7 * n }
