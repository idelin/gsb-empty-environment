# SUCI空保护方案降级

攻击者伪装基站诱导终端用 null 方案发送 SUCI，直接泄露 SUPI。为Go归属网络组件写可导入的 SUCI 准入包：保护方案 0（null）仅当调用方标记紧急注册时接受，1 为 Profile A，2 为 Profile B，其余拒绝；公钥标识须在当前与上一代归属网络密钥表内；密文含临时公钥、密文、MAC，先验 MAC 再解密，重放窗口内重复的临时公钥拒绝；路由指示符为1至4位十进制。解密注入，测降级、错 MAC、重放、旧钥。

## 包接口

包路径为 `example.org/netsec/q0236`，包名为 `netsec`。

```go
ring, err := netsec.NewKeyRing(
    map[int][]byte{1: currentPrivateKey},
    map[int][]byte{2: previousPrivateKey},
)
admission, err := netsec.NewAdmission(
    ring,
    decryptor, // 实现 netsec.Decryptor：VerifyMAC + Decrypt
    netsec.WithReplayWindow(30*time.Second),
)
result, err := admission.Admit(netsec.Request{
    RoutingIndicator:       "1234",
    ProtectionScheme:       netsec.SchemeProfileA,
    HomeNetworkPublicKeyID: 1,
    EmergencyRegistration:  false,
    SchemeOutput:           schemeOutput,
})
```

`Admit` 的检查顺序：

1. 路由指示符必须是 1 至 4 位 ASCII 十进制数字。
2. 保护方案必须是 0、1、2；null 方案要求 `EmergencyRegistration` 为 true。
3. Profile A/B 的公钥标识必须命中当前代或上一代密钥表。
4. Scheme Output 按 `临时公钥 || 密文 || MAC` 切分；Profile A 为 32+8，Profile B 为 33+8 字节固定字段。
5. 先调用 `VerifyMAC`，成功后调用 `Decrypt`；MAC 失败不会触发解密。
6. 仅在 MAC 与解密均成功后登记临时公钥；重放窗口内相同临时公钥再次出现即拒绝。

测试覆盖 null 降级、非法方案、路由指示符、未知/上一代密钥、错 MAC 不触发解密、失败请求不污染重放缓存、并发重放、窗口过期后重试。
