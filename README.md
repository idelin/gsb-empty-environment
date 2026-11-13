# netsec：加密聊天附件封装

`netsec` 是可导入 Go 消息宿主的附件封装包。它把附件切成固定大小的明文块，
逐块使用 AES-256-GCM 加密，并把文件身份、块位置、完整附件形状和 metadata
摘要绑定进 nonce 与认证附加数据（AAD），阻止跨附件拼接和同附件换块。

包不依赖第三方库，只使用 Go 标准库。

## 协议要点

- 每个附件生成随机的 32 字节 `fileID` 和 32 字节 `fileKey`。
- 块号从 0 连续编排；除最后一块外，每块明文长度恰为 `chunkSize`。空附件为
  0 块；长度不是整块倍数时，最后一块为剩余长度。
- 块 nonce：

  ```text
  HMAC-SHA256(fileKey, "nonce" || fileID || uint64_be(chunkIndex))[0:12]
  ```

- 块 AAD 使用固定宽度的大端编码，绑定协议版本、`fileID`、块号、总块数、总
  明文长度、本块明文长度和 `SHA256(metadata)`。
- 清单保存每块密文的 `SHA256`、Merkle 根、`chunkSize`、总长度、metadata
  摘要、接收设备封装项，以及发送设备的 Ed25519 签名。签名同时覆盖
  `sessionID`、`messageID` 和发送设备 ID。
- `fileKey` 使用每个接收设备的 X25519 公钥封装：临时 X25519 密钥 +
  HKDF-SHA256 + AES-256-GCM。封装 AAD 绑定接收设备 ID、附件身份、发送设备
  和会话/消息 ID。
- 清单使用确定性的二进制编码，可调用 `Manifest.MarshalBinary` /
  `Manifest.UnmarshalBinary` 传输。

## 快速使用

```go
import (
    "crypto/ed25519"
    "io"

    netsec "example.org/netsec/q0152"
)

func sendAttachment(source io.Reader) error {
sender, _ := netsec.GenerateDevice("sender-device")
phone, _ := netsec.GenerateDevice("phone-device")

sealed, err := netsec.SealAttachment(source, netsec.SealOptions{
    ChunkSize:      64 * 1024,
    Metadata:       []byte(`{"name":"report.pdf","type":"application/pdf"}`),
    SessionID:      "session-42",
    MessageID:      "message-99",
    SenderDeviceID: sender.DeviceID,
    SigningKey:     sender.SigningKey,
    Recipients:     []netsec.Recipient{phone.Recipient()},
})
if err != nil {
    return err
}

manifestBytes, err := sealed.Manifest.MarshalBinary()
if err != nil {
    return err
}

// 接收端：manifestBytes 和 sealed.Chunks 从消息/附件传输层取得。
var manifest netsec.Manifest
if err := manifest.UnmarshalBinary(manifestBytes); err != nil {
    return err
}
opts := netsec.OpenOptions{
    DeviceID:             phone.DeviceID,
    EncryptionPrivateKey: phone.EncryptionKey,
    TrustedSenders: map[string]ed25519.PublicKey{
        sender.DeviceID: sender.SigningPublicKey(),
    },
    ExpectedSessionID: "session-42",
    ExpectedMessageID: "message-99",
}
if err := netsec.OpenAttachmentToFile(&manifest, sealed.Chunks, "report.pdf", opts); err != nil {
    return err
}
return nil
}
```

也可以使用 `OpenAttachment` 在内存中取得完整明文，或使用 `OpenReader` 按任意
请求顺序逐块校验和解密。`OpenOptions.OnFile` 只在文件通过全部校验并完成原子
发布后调用一次；任何失败路径都不会调用。

## 失败语义

- 清单签名、可信发送设备、会话 ID、消息 ID、metadata 或接收设备封装身份不
  匹配时，在任何块解密前失败。
- 每块先验证密文 `SHA256` 和 Merkle 路径，再执行 AEAD 解密。
- 重复块号、缺块、非末块短块、最后块长度错误、总明文长度不符或任何认证失败
  都会使整个附件失败。
- 流式落盘时，只把已经通过摘要、Merkle 和 AEAD 校验的块写入目标目录内的
  隔离临时文件。全部块完成后才 `fsync`、关闭并 `rename` 到目标路径；失败时
  删除临时对象，不发布部分文件。

## 测试

测试夹具使用两个 `fileKey` 相同但 `fileID` 不同的附件，覆盖跨文件块注入、
同文件换块、截断、重复/缺失块、接收设备唯一性、重复接收设备、metadata
改写、空附件与最后块边界、乱序请求、清单编解码，以及失败时文件回调零调用。

```sh
go test ./...
go test -race ./...
go vet ./...
```
