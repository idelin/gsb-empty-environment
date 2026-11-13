// gsbhost 是教学演示宿主：本地合成同 nonce 观测，
// 用 netsec 包恢复密钥候选并对新消息伪造标签。
// 仅用于安全实验，不对应任何真实协议。
package main

import (
	"fmt"
	"math/big"

	netsec "example.org/netsec/q0023"
)

func main() {
	f := netsec.Field128()

	// 仅宿主本地可见的密钥材料，求根器与伪造器接触不到。
	h, _ := new(big.Int).SetString("deadbeefcafe1234567890abcdef42", 16)
	s, _ := new(big.Int).SetString("0f1e2d3c4b5a69788796a5b4c3d2e1f0", 16)

	// 同 nonce 下的两条观测（消息, 标签）。
	m1 := []*big.Int{f.FromUint(0x11)}
	m2 := []*big.Int{f.FromUint(0x22)}

	g := f.NewForger()
	st, _ := g.AddObservation(m1, f.Tag(h, s, m1))
	fmt.Printf("观测1后: %v\n", st)
	st, _ = g.AddObservation(m2, f.Tag(h, s, m2))
	fmt.Printf("观测2后: %v，候选数=%d\n", st, len(g.Candidates()))

	fresh := []*big.Int{f.FromUint(0xaa), f.FromUint(0xbb)}
	tags, fst, _ := g.Forge(fresh)
	for i, t := range tags {
		fmt.Printf("候选%d: H=%x S=%x 伪造标签=%x\n", i, t.Candidate.H, t.Candidate.S, t.Tag)
	}
	fmt.Printf("伪造判定: %v\n", fst)
}
