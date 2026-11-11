package dictneg_test

import (
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0090/dictneg"
)

// Example 演示会话宿主如何驱动一次完整的字典替换协商。
func Example() {
	store := dictneg.NewMemStore()
	prop, _ := dictneg.NewProposer(dictneg.ProposerConfig{Store: store, Name: "sess"})
	acc, _ := dictneg.NewAcceptor(dictneg.AcceptorConfig{
		Store: dictneg.NewMemStore(), Codec: dictneg.NopCodec{}, Name: "sess",
	})
	sink := func(d dictneg.Digest, seq uint64, plain []byte) {}

	// 1. 安装：发送端发起，接收端落盘后回确认。
	install, _ := prop.BeginInstall([]byte("my-dictionary"))
	ack, _ := acc.HandleEvent(install, sink)
	_ = prop.HandleEvent(ack)

	// 2. 启用：独立的启用事件，接收端切换活动字典并回确认。
	enable, _ := prop.Enable()
	enableAck, _ := acc.HandleEvent(enable, sink)
	_ = prop.HandleEvent(enableAck)

	// 3. 数据帧明确绑定当前字典摘要。
	frame, _ := prop.WrapFrame(0, []byte("compressed"))
	_ = acc.HandleFrame(frame, sink)

	fmt.Println("converged:", prop.ActiveDigest() == acc.ActiveDigest())
	// Output: converged: true
}

// TestZeroDigestFrameRejected 验证无活动字典时零摘要帧被拒绝。
func TestZeroDigestFrameRejected(t *testing.T) {
	acc, err := dictneg.NewAcceptor(dictneg.AcceptorConfig{
		Store: dictneg.NewMemStore(), Codec: dictneg.NopCodec{}, Name: "z",
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := func(dictneg.Digest, uint64, []byte) {}
	err = acc.HandleFrame(dictneg.Frame{Seq: 0, Payload: []byte("x")}, sink)
	if !errors.Is(err, dictneg.ErrNoActiveDictionary) {
		t.Fatalf("want ErrNoActiveDictionary, got %v", err)
	}
}
