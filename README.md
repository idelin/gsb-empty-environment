# 证书用途约束交集

Go内网签发服务需要可导入的委托证书验证包，使用本题自定义证书格式与Ed25519签名，不声称实现X.509。证书含主体公钥、签发者ID、序列号、允许操作集合、资源路径前缀集合、可再委托深度及有效区间左闭右开；签名覆盖长度前缀的全部字段。可信根由宿主固定，输入最多32证书。验证必须找到同一条无环链，逐级权限交集、路径分量前缀交集、剩余深度递减，不能从不同链拼接权限。返回支持请求的完整链；同公钥不同证书不能跳过约束，撤销按签发者和序列号配对。用真实签名测试分叉拼权、前缀相似、深度耗尽、时间边界及合法交叉签发。

## 使用

包路径为 `example.org/netsec/q-021`。证书由签发者私钥签名，根锚点必须同时匹配宿主配置的 ID 和公钥。

```go
cert := netsec.Certificate{
    SubjectPublicKey:   leafPublicKey,
    IssuerID:           "root",
    Serial:             1,
    AllowedOperations:  []string{"read"},
    ResourcePrefixes:   []string{"/data/project"},
    MaxDelegationDepth: 0,
    NotBefore:          notBefore,
    NotAfter:           notAfter,
}
if err := netsec.SignCertificate(&cert, rootPrivateKey); err != nil {
    return err
}

chain, err := netsec.Verify(
    []netsec.Certificate{cert},
    []netsec.TrustRoot{{ID: "root", PublicKey: rootPublicKey}},
    netsec.Request{
        Operation:        "read",
        ResourcePath:     "/data/project/report",
        SubjectPublicKey: leafPublicKey,
    },
    verificationTime,
    nil,
)
```

## 验证语义

- 签名消息带协议域，全部证书字段按固定顺序编码并带长度前缀；签名本身不进入消息。集合字段在签名前排序去重。
- 只使用一条链上的证书计算权限。操作取交集，路径前缀按 `/` 分量取交集，绝不从兄弟证书拼接权限。
- `MaxDelegationDepth` 每下降一级递减，并受父链剩余深度和子证书声明值共同限制。
- 链中所有证书都必须在验证时刻有效，区间为左闭右开。链内主体公钥不得重复。
- 撤销键严格为 `(IssuerID, Serial)`；相同序列号的不同签发者互不影响。
- 根签发的第一张证书必须匹配根 ID 并验签成功。返回链从该证书到叶证书，不包含宿主根配置。
- 请求指定 `SubjectPublicKey` 时，返回链必须终止于该公钥对应的证书；同一公钥存在多张不同约束的证书时，会逐张作为候选叶验证。
- 单次输入最多 32 张证书。路径必须是绝对规范路径，仅接受一个尾部 `/`。

测试全部使用真实 Ed25519 签名，覆盖合法多级链、分叉拼权、相似前缀、深度耗尽、时间端点、交叉签发、同公钥多证书、撤销配对和字段篡改。
