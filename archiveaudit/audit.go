package main

import (
	"fmt"
	"io"
	"math/big"
)

// RunAudit 执行一次抽样审计：输出完整抽样过程，再逐块验证摘要。
// 返回 true 表示所有抽中块摘要匹配。
//
// 注意措辞：抽样通过只代表被抽中的块完整，绝不能写成全文件完整。
func RunAudit(a *Archive, seed string, k int, out io.Writer) bool {
	total := a.TotalLength()
	fmt.Fprintf(out, "归档共 %d 块，总逻辑长度 %s\n", len(a.Blocks), total.String())
	fmt.Fprintf(out, "种子: %q，计划抽样 %d 块（按逻辑长度加权、无放回）\n\n", seed, k)

	picked, records, err := Sample(a, seed, k)
	if err != nil {
		fmt.Fprintf(out, "抽样失败: %v\n", err)
		return false
	}

	fmt.Fprintln(out, "== 抽样过程 ==")
	sampledWeight := new(big.Int)
	for _, r := range records {
		fmt.Fprintf(out, "第 %d 抽: 剩余总权重=%s 随机值=%s -> 选中块 %s (权重 %s)\n",
			r.Step, r.Remaining.String(), r.Roll.String(), r.BlockID, r.Weight.String())
		sampledWeight.Add(sampledWeight, r.Weight)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "== 摘要验证 ==")
	allOK := true
	for _, b := range picked {
		if b.VerifyDigest() {
			fmt.Fprintf(out, "块 %s: 摘要匹配 OK\n", b.ID)
		} else {
			fmt.Fprintf(out, "块 %s: 摘要不匹配 FAIL\n", b.ID)
			allOK = false
		}
	}
	fmt.Fprintln(out)

	pct := new(big.Rat).SetFrac(sampledWeight, total)
	f, _ := pct.Float64()
	fmt.Fprintf(out, "本次抽样覆盖 %d/%d 块，覆盖逻辑长度占比 %.4f%%\n",
		k, len(a.Blocks), f*100)
	if allOK {
		if k == len(a.Blocks) {
			fmt.Fprintln(out, "结论: 全部块均被抽中且摘要匹配，归档各块完整。")
		} else {
			fmt.Fprintln(out, "结论: 抽样校验通过——仅被抽中的块摘要全部匹配；"+
				"未抽中的块未经验证，本结果不代表全文件完整。")
		}
	} else {
		fmt.Fprintln(out, "结论: 抽样校验失败——存在摘要不匹配的块，归档已损坏。")
	}
	return allOK
}
