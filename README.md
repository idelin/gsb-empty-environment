# TLS握手消息顺序与提前ChangeCipherSpec

密钥交换完成前塞入 ChangeCipherSpec，或跳过 Certificate，客户端可能用空主密钥或未认证的密钥继续。为Go TLS 1.2 客户端写可导入的握手状态机包：按 ServerHello、Certificate、ServerKeyExchange（仅 ECDHE）、可选 CertificateRequest、ServerHelloDone、CCS、Finished 接收，CCS 只能在本端发出 ClientKeyExchange 之后出现；乱序、重复、缺失一律以 unexpected_message 中止且不启用加密。用提前 CCS、缺 Certificate 和重复 ServerHello 验证。

## 包：`example.org/netsec/q0834`（包名 `netsec`）

`TLS12ClientHandshake` 只做握手消息的顺序/状态校验，不参与密码学运算，方便嵌入自定义 record layer。

```go
import netsec "example.org/netsec/q0834"

m := netsec.NewTLS12ClientHandshake()

// 逐个喂入握手消息（type 为 RFC 5246 7.4 的握手类型字节）
m.HandleHandshake(netsec.HandshakeServerHello, body)
m.HandleHandshake(netsec.HandshakeCertificate, body)
m.HandleHandshake(netsec.HandshakeServerKeyExchange, body) // 仅 ECDHE
// 可选：m.HandleHandshake(netsec.HandshakeCertificateRequest, body)
m.HandleHandshake(netsec.HandshakeServerHelloDone, body)

// 本端真正发出 ClientKeyExchange 之后再通知状态机
m.NotifyClientKeyExchangeSent()

// 只有上面的通知发生后，入站 CCS 才会被接受
if err := m.HandleChangeCipherSpec([]byte{1}); err != nil {
    // err 为 *netsec.HandshakeError，Alert == AlertUnexpectedMessage
    // 此时 m.State() == StateFailed，m.ReadEncrypted() == false
}
m.HandleHandshake(netsec.HandshakeFinished, body) // 加密阶段仍由状态机收口
```

也可以直接喂 record：`m.HandleRecord(contentType, fragment)`，handshake record 需为标准的 `type(1) + length(3) + body` 片段。

## 安全语义

- 入站顺序固定为 `ServerHello -> Certificate -> ServerKeyExchange -> [CertificateRequest] -> ServerHelloDone -> CCS -> Finished`。
- 提前、重复、错位的 CCS，以及缺失/重复/乱序的握手消息，统一返回 `AlertUnexpectedMessage` 并进入终态 `StateFailed`。
- `ReadEncrypted()` 仅在合法 CCS 被接受后置 true；任何中止发生在 CCS 之前时加密都不会启用，避免空主密钥或未认证密钥继续通信。
- 零值机器需先 `Start()`；也可直接用 `NewTLS12ClientHandshake()`。机器非并发安全。

## 验证

`handshake_fsm_test.go` 覆盖题目要求的三类攻击与正常路径：

- 提前 CCS：握手起始、ServerHello 后、证书/SKE 中途、SHD 后但未发 CKE，以及重复 CCS、畸形 CCS。
- 缺 Certificate：直接送 ServerKeyExchange 或 ServerHelloDone 均被拒绝。
- 重复 ServerHello：第二个 ServerHello 立即中止，后续消息继续被拒。

运行：

```sh
go test ./... -race
```
