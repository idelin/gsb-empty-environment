package deltacrdt

import (
	"fmt"
	"testing"
)

// buildDoc 构造一个包含 n 个字符的文档。
func buildDoc(site uint64, n int) *Document {
	doc := NewDocument(site)
	parent := rootID
	for i := 0; i < n; i++ {
		parent = doc.InsertAfter(parent, rune('a'+i%26))
	}
	return doc
}

// TestDeltaSizeConstantForSingleCharEdit 验收核心：
// 10000 字符文档中仅修改 1 个字符，Delta 包必须是常数级字节。
func TestDeltaSizeConstantForSingleCharEdit(t *testing.T) {
	const docSize = 10000
	const deltaBound = 128 // 常数级上界（字节）

	alice := NewReplica(1)
	bob := NewReplica(2)
	alice.Doc = buildDoc(1, docSize)

	// 首次全量同步，Bob 回传 Ack，Alice 记录 Bob 的水位。
	fullBytes := alice.SyncTo(bob)
	t.Logf("全量状态包大小: %d 字节 (%d 字符)", fullBytes, docSize)

	// Alice 仅修改 1 个字符。
	alice.Doc.ReplaceAt(docSize/2, 'Z')

	// 生成增量 Delta 并传输。
	deltaBytes := alice.SyncTo(bob)
	t.Logf("修改 1 字符后的 Delta 包大小: %d 字节", deltaBytes)

	if deltaBytes > deltaBound {
		t.Fatalf("Delta 包 %d 字节，超过常数级上界 %d 字节", deltaBytes, deltaBound)
	}

	// Delta 大小必须与文档规模无关：用 10 倍规模文档重验。
	alice2 := NewReplica(11)
	bob2 := NewReplica(12)
	alice2.Doc = buildDoc(11, docSize*10)
	alice2.SyncTo(bob2)
	alice2.Doc.ReplaceAt(docSize*5, 'Z')
	deltaBytes2 := alice2.SyncTo(bob2)
	t.Logf("10 倍规模文档修改 1 字符的 Delta 包大小: %d 字节", deltaBytes2)
	if deltaBytes2 != deltaBytes {
		t.Fatalf("Delta 大小随文档规模变化: %d vs %d", deltaBytes, deltaBytes2)
	}
}

// TestDeltaEquivalenceWithFullState 验收核心：
// 接收端应用 Delta 后的状态与全量传输完全等价。
func TestDeltaEquivalenceWithFullState(t *testing.T) {
	const docSize = 10000

	alice := NewReplica(1)
	alice.Doc = buildDoc(1, docSize)

	// Bob 走增量通道：全量同步 -> 修改 -> Delta 同步。
	bob := NewReplica(2)
	alice.SyncTo(bob)
	alice.Doc.ReplaceAt(123, 'X')
	alice.Doc.DeleteAt(456)
	alice.Doc.InsertAt(789, 'Y')
	alice.SyncTo(bob)

	// Carol 走全量通道：直接传输最终全量状态。
	carol := NewReplica(3)
	wire := alice.FullStateFor().Encode()
	delta, err := DecodeDelta(wire)
	if err != nil {
		t.Fatal(err)
	}
	carol.Receive(delta)

	if bob.Doc.String() != alice.Doc.String() {
		t.Fatal("增量同步后文本与源不一致")
	}
	if carol.Doc.String() != alice.Doc.String() {
		t.Fatal("全量同步后文本与源不一致")
	}
	if bob.Doc.String() != carol.Doc.String() {
		t.Fatal("增量同步与全量传输结果不等价")
	}
	if !bob.Doc.Version().Equal(alice.Doc.Version()) {
		t.Fatal("增量同步后版本向量与源不一致")
	}
	if !carol.Doc.Version().Equal(alice.Doc.Version()) {
		t.Fatal("全量同步后版本向量与源不一致")
	}
}

// TestAckWatermarkSuppressesRedundantDelta 验证：
// 无新变动时，已确认水位使 Delta 为空包。
func TestAckWatermarkSuppressesRedundantDelta(t *testing.T) {
	alice := NewReplica(1)
	bob := NewReplica(2)
	alice.Doc = buildDoc(1, 1000)

	alice.SyncTo(bob)

	delta := alice.DeltaFor(bob.ID)
	if len(delta.Entries) != 0 {
		t.Fatalf("无变动时应生成空 Delta，实际 %d 条", len(delta.Entries))
	}
	if size := len(delta.Encode()); size != 4 {
		t.Fatalf("空 Delta 编码应仅含 4 字节计数头，实际 %d 字节", size)
	}
}

// TestConcurrentEditsConverge 验证并发编辑下双方向交换 Delta 后收敛。
func TestConcurrentEditsConverge(t *testing.T) {
	alice := NewReplica(1)
	bob := NewReplica(2)
	alice.Doc = buildDoc(1, 100)
	alice.SyncTo(bob)

	// 双方并发编辑（无通信）。
	alice.Doc.InsertAt(0, 'A')
	alice.Doc.DeleteAt(50)
	bob.Doc.InsertAt(0, 'B')
	bob.Doc.ReplaceAt(99, 'Z')

	// 双向交换 Delta。
	alice.SyncTo(bob)
	bob.SyncTo(alice)
	// 再各同步一轮，保证水位对齐后无残余差异。
	alice.SyncTo(bob)
	bob.SyncTo(alice)

	if alice.Doc.String() != bob.Doc.String() {
		t.Fatalf("并发编辑未收敛:\nalice=%q\nbob  =%q", alice.Doc.String(), bob.Doc.String())
	}
	if !alice.Doc.Version().Equal(bob.Doc.Version()) {
		t.Fatal("并发编辑后版本向量不一致")
	}
}

// TestDeltaIdempotentAndCommutative 验证 Delta 应用幂等、乱序可交换。
func TestDeltaIdempotentAndCommutative(t *testing.T) {
	alice := NewReplica(1)
	alice.Doc = buildDoc(1, 500)
	alice.Doc.ReplaceAt(10, 'Q')
	alice.Doc.InsertAt(20, 'W')

	full := alice.Doc.GenerateDelta(VersionVector{})
	mid := len(full.Entries) / 2
	first := &Delta{Entries: full.Entries[:mid]}
	second := &Delta{Entries: full.Entries[mid:]}

	// Bob 正序、Carol 逆序、Dave 重复应用。
	bob := NewReplica(2)
	bob.Receive(first)
	bob.Receive(second)

	carol := NewReplica(3)
	carol.Receive(second)
	carol.Receive(first)

	dave := NewReplica(4)
	dave.Receive(full)
	dave.Receive(full)

	want := alice.Doc.String()
	for name, doc := range map[string]*Document{
		"正序": bob.Doc, "逆序": carol.Doc, "重复": dave.Doc,
	} {
		if doc.String() != want {
			t.Fatalf("%s应用结果不一致", name)
		}
	}
}

// TestEncodeDecodeRoundTrip 验证二进制编解码往返一致。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	doc := buildDoc(7, 100)
	doc.DeleteAt(3)
	doc.ReplaceAt(50, '界')

	delta := doc.GenerateDelta(VersionVector{})
	decoded, err := DecodeDelta(delta.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Entries) != len(delta.Entries) {
		t.Fatalf("条目数不一致: %d vs %d", len(decoded.Entries), len(delta.Entries))
	}
	for i := range delta.Entries {
		if decoded.Entries[i] != delta.Entries[i] {
			t.Fatalf("第 %d 条不一致: %+v vs %+v", i, decoded.Entries[i], delta.Entries[i])
		}
	}
}

// ExampleReplica_SyncTo 演示两个节点间的增量同步流程。
func ExampleReplica_SyncTo() {
	alice := NewReplica(1)
	bob := NewReplica(2)

	for _, ch := range "hello world" {
		alice.Doc.InsertAt(alice.Doc.Len(), ch)
	}
	full := alice.SyncTo(bob)

	alice.Doc.ReplaceAt(0, 'H')
	delta := alice.SyncTo(bob)

	fmt.Printf("全量同步: %d 字节\n", full)
	fmt.Printf("改 1 字符增量: %d 字节\n", delta)
	fmt.Printf("Bob 看到: %s\n", bob.Doc.String())
	// Output:
	// 全量同步: 411 字节
	// 改 1 字符增量: 94 字节
	// Bob 看到: Hello world
}
