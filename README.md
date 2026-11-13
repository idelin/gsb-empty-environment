# SEIPD 分块序号

OpenPGP v2 SEIPD 分块 AEAD 的可导入 Go 实现，用于邮件解密调用。

每个分块的 nonce 由消息 IV 与 64 位大端序号异或得到，关联数据同时绑定包版本、算法、分块大小和序号。普通分块从 0 开始递增，最后一块使用 `FinalSequence`（全 1）结束标志。序号跳跃、倒退或重复的结束标志都会失败。

默认策略 `AllowSequenceReuse` 保留题目描述的序号重用行为：非末尾分块可以重复前一块的序号。重复序号会同时复用 nonce 与关联数据，因此序号相同的两个分块可以互换。把前一块的 tag 和密文移到后一个复用序号的位置，后一块会解出前一块的明文。`RequireSequential` 提供严格校验，拒绝任何序号重用并要求结束标志存在。

## 快速使用

```go
package main

import (
	"fmt"
	"example.org/netsec/q0296"
)

func main() {
	sessionKey := make([]byte, 32)

	message, err := netsec.EncryptMessage(sessionKey, []byte("mail body"),
		netsec.Config{ChunkSize: 1 << 20})
	if err != nil {
		panic(err)
	}

	packet, err := message.MarshalBinary()
	if err != nil {
		panic(err)
	}

	plaintext, err := netsec.Decrypt(sessionKey, packet)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s\n", plaintext)
}
```

## 分块 API

```go
c, err := netsec.NewCipher(sessionKey, netsec.Config{
	ChunkSize: 64,
	Policy:    netsec.AllowSequenceReuse,
})

first, _ := c.SealChunk(0, false, []byte("FIRST"))
second, _ := c.SealChunk(0, false, []byte("SECOND")) // 重用序号 0
last, _ := c.SealFinal([]byte("END"))

chunks := []netsec.Chunk{first, second, last}
_ = netsec.SwapPayloads(chunks, 0, 1)
plaintext, _ := c.Decrypt(chunks) // "SECONDFIRSTEND"
```

其他辅助函数：

- `SwapSequenceNumbers` / `SwapChunkSequences` / `SwapSequences`：交换两个分块的序号。
- `SwapChunks`：交换两个完整分块。
- `SwapPayloads`：只交换 tag 与密文，保留序号和 nonce。
- `SetSequence`：修改单个分块的序号。
- `ReuseSequence` / `ReuseNonce`：把一个分块（序号、nonce、tag、密文）复制到后一个位置，直接复现“后一块解出前一块明文”。
- `ValidateSequences`：按策略校验分块序号。
- `EncryptIndexed` / `ParseIndexed`：保留显式序号的可序列化格式，便于测试序号重用和交换。
- `EncryptPacket` / `DecryptPacket` / `WrapSEIPD` / `UnwrapSEIPD`：处理带 Tag 18 包头的完整 OpenPGP 包。

严格模式示例：

```go
plaintext, err := c.Decrypt(chunks) // c.Policy == netsec.RequireSequential
// err 为 netsec.ErrSequenceReuse
```

## 测试

```sh
go test ./...
```
