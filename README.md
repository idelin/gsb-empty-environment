# IKEv2身份与证书绑定

IKE_AUTH里，攻击者持有一张合法证书，可在IDi中声明另一个身份来套用更宽的访问策略。给Go IKEv2响应方写供SA授权环节调用的身份绑定包：按RFC 7296第2.15节重建发起方签名八位组，其中含第一条消息、响应方Nonce和以SK_pi对IDi载荷体计算的PRF；IDi的类型与值必须与证书SAN或主题匹配，通过后才取该身份的流量选择器策略，CHILD_SA提议只能缩小不能超出。用错配IDi、篡改IDr、证书正常但身份越权和合法协商验证。

## 使用

`idbind` 包在响应方收到 IKE_AUTH 后由 SA 授权环节调用：

```go
id, policy, err := idbind.Authorize(&idbind.AuthRequest{
    RealMessage1:  ikeSaInitRequest, // 线上原样收到的第一条消息
    NonceR:        nonceR,
    IDiPayload:    idiPayload,       // 含4字节通用头
    IDrPayload:    idrPayload,
    SKPi:          skPi,
    PRF:           idbind.PRFHMACSHA256,
    AuthMethod:    idbind.AuthECDSASha256,
    AuthSignature: authPayload,
    PeerCert:      peerCert,         // 已完成证书链校验
    OwnID:         ownID,
    TSi:           proposedTSi,
    TSr:           proposedTSr,
}, policyStore)
```

校验顺序为失败即拒：AUTH签名（RFC 7296 §2.15 签名八位组）→ IDr 必须指向本响应方 → IDi 与证书 SAN/主题绑定 → 按已认证身份取策略 → CHILD_SA 流量选择器只能收窄不得越界。

运行测试：`go test ./...`
