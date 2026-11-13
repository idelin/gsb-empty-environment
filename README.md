# SRv6 SRH的HMAC TLV校验

域边界上外部来源的 SRv6 报文若带 SRH，攻击者可借它把流量绕到任意段列表。为Go转发面写可导入的 SRH 准入包（RFC 8754）：来自域外的带 SRH 报文必须带 HMAC TLV 且 Key ID 非 0；HMAC-SHA256 覆盖源地址、Last Entry、Flags、2 字节保留零、Key ID 和整个段列表；Segments Left 大于 Last Entry 加 1 时丢弃。用改段列表、Key ID 为 0、篡改源地址和合法报文验证。

## 用法

准入逻辑位于可导入子包 `srh`（导入路径 `example.org/netsec/q0749/srh`）：

```go
keys := srh.NewKeyring()
if err := keys.Add(keyID, sharedKey); err != nil { /* ... */ }

// 域边界入向：无 SRH 放行；带 SRH 必须通过 HMAC TLV 校验。
if err := srh.Admit(rawIPv6Packet, keys); err != nil {
    // 丢弃报文，err 包装 ErrNoHMAC / ErrZeroKeyID / ErrBadMAC /
    // ErrUnknownKey / ErrSegmentsLeft / ErrMalformed / ErrNotIPv6
}

// 出向：为 SRH 追加 HMAC TLV 并计算 HMAC-SHA-256。
signed, err := srh.Sign(rawIPv6Packet, keyID, sharedKey)
```

HMAC-SHA256 输入为：源地址(16B) || Last Entry(1B) || Flags(1B) ||
保留(2B，恒零) || Key ID(4B，大端) || 整个段列表（每个 SID 16B，按报文顺序）。
