# CAPWAP端点身份绑定

无线控制器通过CAPWAP DTLS接受AP加入，攻击者拿到一台合法AP的证书后，可在加入请求里冒用另一台AP的标识，或在握手后伪造源地址接管控制通道。给Go控制器写供Join处理调用的端点绑定包：DTLS证书指纹须与加入请求中的WTP Board Data序列号及MAC一致，绑定键为证书、WTP标识、会话ID和DTLS连接；控制消息只在已认证DTLS记录内且序号落入重放窗口时处理，地址迁移只能经新的认证记录，迁移后旧地址的数据报立即丢弃。用冒用标识、重放记录、地址漂移和重复加入验证。

## 实现

根包 `netsec` 提供 Join 处理所需的端点绑定 API：

- `FingerprintOfCertificate` / `FingerprintOfDER`：对证书 DER 计算 SHA-256 指纹。
- `Enrollment`：控制器可信映射，把证书指纹绑定到 WTP 标识、Board Data 序列号和 MAC。
- `Binder.BindJoin`：校验指纹、WTP 标识、序列号和 MAC，并创建以 `(证书, WTP, 会话 ID, DTLS 连接)` 为键的 `Binding`。
- `Binding.Process`：只接受 `Authenticated` 为真的 DTLS 记录，并按 epoch 与滑动重放窗口校验序号。
- 地址迁移：新源地址上的**新鲜且已认证**记录会把绑定迁移到该地址；原地址立即进入退役集合，后续数据报在重放窗口之前即被丢弃。

```go
fp := netsec.FingerprintOfCertificate(certFromDTLS)
binder := netsec.NewBinder(netsec.Enrollment{
    Fingerprint:  fp,
    WTPID:        "wtp-7",
    SerialNumber: "SN-0007",
    MAC:          macFromBoardData,
})

binding, err := binder.BindJoin(netsec.JoinRequest{
    Certificate: certFromDTLS,
    WTPID:       "wtp-7",
    SessionID:   42,
    Connection:  "dtls-handshake-42",
    Source:      apSourceAddr,
    Board: netsec.WTPBoardData{
        SerialNumber: "SN-0007",
        MAC:          macFromBoardData,
    },
})

message, err := binding.Process(netsec.NewAuthenticatedRecord(
    "dtls-handshake-42", sourceFromUDPPacket, epoch, recordSequence, controlPayload,
))
```

## 集成约束

- `Record.Authenticated` 只能由 DTLS 层在校验记录完整性标签并完成解密后设置；未认证记录一律拒绝。
- `Connection` 必须为每次 DTLS 握手生成新的唯一标识，不能复用 MAC、WTP 标识或证书指纹。
- 同一 `Connection` 只能承载一个活跃绑定；证书、WTP 标识和会话 ID 也不能在多个绑定点之间复用。
- 每个绑定独立维护重放窗口；epoch 前进时窗口重置，旧 epoch 的记录立即拒绝。
- Join 时若已有同一证书、WTP 标识或会话 ID 的活跃绑定，则拒绝重复/冲突加入。需要重新加入时，控制器应先关闭旧绑定。
- 本包只做证书指纹到可信设备身份的映射校验；证书链、有效期和 DTLS 密钥验证由调用方的 PKI/DTLS 层完成。

## 验证

`go test ./...` 覆盖：

- 合法证书冒用其他 Board Data 或 WTP 标识；
- 重复加入、同证书换连接/会话、WTP 或会话 ID 冲突；
- 已认证记录的重放、窗口外旧序号和乱序窗口内序号；
- 经未认证或重放记录尝试地址迁移；
- 地址漂移后旧地址数据报立即丢弃，以及旧 epoch 迁移失败。
