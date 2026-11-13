# STUN消息完整性与属性顺序

STUN 消息里 MESSAGE-INTEGRITY 之后的属性不受完整性保护，攻击者截获合法请求，在其后追加改写地址的属性，服务端若照单全收，就会把流量中继到攻击者指定的对端。为Go ICE 与 TURN 服务写可导入的 STUN 消息校验包，输入消息字节与长期凭据密钥：头部 20 字节，长度字段须等于属性总长度且是 4 的倍数，魔数 0x2112A442 不符拒绝；属性按 TLV 解析，值按 4 字节对齐补零，补位字节须为 0；校验 MESSAGE-INTEGRITY（0x0008）时，HMAC-SHA1 的输入是头部到该属性之前的字节，其中头部长度字段按截止到该属性末尾重算；该属性之后只允许 MESSAGE-INTEGRITY-SHA256（0x001C）与 FINGERPRINT（0x8028），其他属性一律忽略，不得进入业务处理；FINGERPRINT 为 CRC-32 异或 0x5354554E，且须是最后一个属性；MESSAGE-INTEGRITY 出现两次、长度不是 20 或属性长度越界时拒绝。用完整性之后追加 XOR-PEER-ADDRESS、长度字段不符、补位非零、FINGERPRINT 不在末尾和合法请求验证。

## 包 `stun`

导入路径：`example.org/netsec/q0668/stun`

### 用法

```go
import "example.org/netsec/q0668/stun"

// key 为长期凭据推导出的 HMAC 密钥（RFC 5389 的 HMAC key 本身）。
msg, err := stun.Validate(rawDatagram, key)
if err != nil {
    // 用 errors.Is 匹配 stun.ErrIntegrityMismatch、stun.ErrBadMessageLength 等哨兵错误。
    return err
}

// 只有 MESSAGE-INTEGRITY 之前的属性会出现在 msg.Attrs 中；
// 完整性之后追加的 XOR-PEER-ADDRESS 等属性已被剥离，业务只信任 msg.Attrs。
for _, attr := range msg.Attrs {
    handleAttribute(attr.Type, attr.Value)
}
```

### 校验保证

- 头部 20 字节；长度字段等于属性区总长度且为 4 的倍数；魔数必须为 `0x2112A442`。
- TLV 属性边界完整，属性值按 4 字节对齐，补位字节必须为 0，否则 `ErrBadPadding`。
- `MESSAGE-INTEGRITY`（`0x0008`）恰好出现一次、长度为 20；HMAC-SHA1 输入为头部到该属性之前的字节，头部长度字段按截止到该属性末尾重算；密钥不匹配返回 `ErrIntegrityMismatch`。
- 完整性之后仅放行 `MESSAGE-INTEGRITY-SHA256`（`0x001C`）与 `FINGERPRINT`（`0x8028`）；其他属性不计入 `Message.Attrs`，仅在 `IgnoredAfterIntegrity` 中计数。
- `FINGERPRINT` 为 CRC-32（IEEE）异或 `0x5354554E`，必须是最后一个属性，否则 `ErrFingerprintNotLast`；CRC 不符返回 `ErrBadFingerprint`。
- 属性 TLV 越界返回 `ErrAttributeOverflow`；输入消息不会被修改。

### 测试

```sh
go test ./...
```

覆盖：合法请求（含/不含 FINGERPRINT）、完整性之后追加 XOR-PEER-ADDRESS 被剥离、长度字段不符、补位非零、FINGERPRINT 不在末尾、魔数错误、密钥错误、完整性重复/长度错误、属性越界、缺完整性、CRC 错误与输入不可变。
