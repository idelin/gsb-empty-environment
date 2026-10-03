// 运行样例：模拟两个网络节点间的 Delta-CRDT 增量同步。
// 用法: go run ./cmd/example
package main

import (
	"fmt"

	"deltacrdt"
)

func main() {
	const docSize = 10000

	alice := deltacrdt.NewReplica(1)
	bob := deltacrdt.NewReplica(2)

	// Alice 构造 10000 字符文档。
	for i := 0; i < docSize; i++ {
		alice.Doc.InsertAt(alice.Doc.Len(), rune('a'+i%26))
	}

	// 首次全量同步（新节点加入）。
	full := alice.SyncTo(bob)
	fmt.Printf("[首次同步] 全量状态包: %d 字节 (%d 字符文档)\n", full, docSize)

	// Alice 仅修改 1 个字符。
	alice.Doc.ReplaceAt(docSize/2, 'Z')

	// 增量同步：仅发送自 Bob 上次确认水位以来的差异切片。
	delta := alice.SyncTo(bob)
	fmt.Printf("[增量同步] 修改 1 字符的 Delta 包: %d 字节 (压缩率 %.1f%%)\n",
		delta, 100*float64(delta)/float64(full))

	// 无变动时：水位抑制冗余传输，仅发空包心跳。
	idle := alice.SyncTo(bob)
	fmt.Printf("[空闲心跳] 无变动 Delta 包: %d 字节\n", idle)

	// 验证等价性：Bob 增量结果 vs 第三方全量结果。
	carol := deltacrdt.NewReplica(3)
	wire := alice.FullStateFor().Encode()
	state, _ := deltacrdt.DecodeDelta(wire)
	carol.Receive(state)
	fmt.Printf("[等价性] Bob(增量) == Carol(全量): %v, 文本一致: %v\n",
		bob.Doc.Version().Equal(carol.Doc.Version()),
		bob.Doc.String() == carol.Doc.String())
}
