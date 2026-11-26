STUN 消息里 MESSAGE-INTEGRITY 之后的属性不受完整性保护，攻击者截获合法请求，在其后追加改写地址的属性，服务端若照单全收，就会把流量中继到攻击者指定的对端。为Go ICE 与 TURN 服务写可导入的 STUN 消息校验包，输入消息字节与长期凭据密钥：头部 20 字节，长度字段须等于属性总长度且是 4 的倍数，魔数 0x2112A442 不符拒绝；属性按 TLV 解析，值按 4 字节对齐补零，补位字节须为 0；校验 MESSAGE-INTEGRITY（0x0008）时，HMAC-SHA1 的输入是头部到该属性之前的字节，其中头部长度字段按截止到该属性末尾重算；该属性之后只允许 MESSAGE-INTEGRITY-SHA256（0x001C）与 FINGERPRINT（0x8028），其他属性一律忽略且禁止进入后续业务解析或覆盖先前同名属性；FINGERPRINT 为 CRC-32 异或 0x5354554E，且须是最后一个属性；MESSAGE-INTEGRITY 或 FINGERPRINT 出现两次、长度不符或属性长度越界时拒绝。用完整性之后追加 XOR-PEER-ADDRESS、长度字段不符、补位非零、FINGERPRINT 不在末尾和合法请求验证。

## stunverify 包

实现位于 `stunverify/`，仅依赖 Go 标准库，导入路径：

```
prescreen.local/ps-61c8233145/stunverify
```

### 安全边界

- 只返回 `MESSAGE-INTEGRITY` 之前的属性，这些属性处于 HMAC 保护范围内，可安全进入 ICE/TURN 业务解析。
- `MESSAGE-INTEGRITY` 之后出现 `MESSAGE-INTEGRITY-SHA256`、`FINGERPRINT` 之外的任何属性（例如攻击者追加的 `XOR-PEER-ADDRESS`）直接拒绝整条消息，非法属性不会覆盖任何先前同名属性。
- 拒绝时返回 `nil` 结果与哨兵错误，业务层无需自行做“忽略未知尾部属性”的判断。

### 用法

```go
import "prescreen.local/ps-61c8233145/stunverify"

func handleSTUN(msg []byte, longTermKey []byte) error {
    res, err := stunverify.Verify(msg, longTermKey)
    if err != nil {
        // errors.Is 可匹配 stunverify.ErrAttributeAfterIntegrity、
        // stunverify.ErrBadIntegrity、stunverify.ErrBadFingerprint 等哨兵。
        return err
    }

    // 只有 res.Attributes 里的属性是经过完整性保护、可信的。
    if username, ok := res.Attribute(0x0006 /* USERNAME */); ok {
        _ = username.Value
    }
    _ = res.TransactionID
    return nil
}
```

### 校验规则

- 头部必须满 20 字节；魔数必须是 `0x2112A442`；长度字段必须等于属性区实际长度且为 4 的倍数。
- 属性按 TLV 解析，值按 4 字节对齐，补位字节必须全为 0；属性长度越界即拒绝。
- `MESSAGE-INTEGRITY` 长度必须为 20，按 RFC 5389 §15.4 重算头部长度字段后做 HMAC-SHA1；`MESSAGE-INTEGRITY-SHA256` 同理（HMAC-SHA256，长度 32）。
- `FINGERPRINT` 长度必须为 4，按 RFC 5389 §15.5 对剔除自身后的消息做 CRC-32 并与 `0x5354554E` 异或比较，且必须是最后一个属性。
- `MESSAGE-INTEGRITY`、`MESSAGE-INTEGRITY-SHA256`、`FINGERPRINT` 任一出现两次即拒绝；缺少 `MESSAGE-INTEGRITY` 拒绝。

### 测试

`go test ./...` 覆盖：合法请求（含/不含 SHA256 与 FINGERPRINT）、完整性之后追加 `XOR-PEER-ADDRESS`、长度字段不符、补位非零、FINGERPRINT 不在末尾/重复、完整性重复或长度错误、属性越界、魔数错误、HMAC 与 CRC 失败，以及 RFC 5389 附录 B.2 风格消息的完整链路。
