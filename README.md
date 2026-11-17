# 证书用途约束交集

Go内网签发服务需要可导入的委托证书验证包，使用本题自定义证书格式与Ed25519签名，不声称实现X.509。证书含主体公钥、签发者ID、序列号、允许操作集合、资源路径前缀集合、可再委托深度及有效区间左闭右开；签名覆盖长度前缀的全部字段。可信根由宿主固定，输入最多32证书。验证必须找到同一条无环链，逐级权限交集、路径分量前缀交集、剩余深度递减，不能从不同链拼接权限。返回支持请求的完整链；同公钥不同证书不能跳过约束，撤销按签发者和序列号配对。用真实签名测试分叉拼权、前缀相似、深度耗尽、时间边界及合法交叉签发。

## 使用

```go
rootAnchor := netsec.TrustAnchor{ID: "root", PublicKey: rootPublicKey}

parent := netsec.NewCertificate(
	serverPublicKey, "root", 1,
	[]string{"read", "write"}, []string{"/srv"}, 2,
	notBefore, notAfter,
)
_ = parent.Sign(rootPrivateKey)

leaf := netsec.NewCertificate(
	clientPublicKey, parent.SubjectID(), 2,
	[]string{"read"}, []string{"/srv/app"}, 0,
	notBefore, notAfter,
)
_ = leaf.Sign(serverPrivateKey)

chain, err := netsec.Verify(netsec.Request{
	Operation:        "read",
	Resource:         "/srv/app/report",
	At:               time.Now(),
	SubjectPublicKey: clientPublicKey,
}, []netsec.Certificate{*parent, *leaf}, []netsec.TrustAnchor{rootAnchor})
```

成功时 `chain` 按叶到根排列；可信根是宿主配置，不在链中。`VerifyResult`
还会返回整条链的有效策略（操作交集、路径前缀交集和剩余深度）。

## 验证规则

- 每条证书边都验证 Ed25519 签名。根证书的签发者必须是宿主固定的可信根。
- 只使用一条链：操作集合和资源前缀逐级求交，不跨分支合并权限。
- 资源前缀按 `/` 分量匹配。`/srv/app` 匹配 `/srv/app/v1`，不匹配
  `/srv/application`。
- 证书有效区间为 `[NotBefore, NotAfter)`。`NotBefore` 时刻有效，
  `NotAfter` 时刻无效。
- `MaxDelegationDepth` 表示该主体还能再签发几级证书。每下降一级先扣减，
  再受子证书声明深度限制。
- 撤销项按 `(IssuerID, Serial)` 精确配对；同一序列号在不同签发者下互不影响。
- 链中不能重复主体公钥。相同公钥的不同证书仍作为不同节点参与搜索，
  因而不能把一条证书的权限与另一条证书的路径前缀拼在一起。
- 一次验证最多接受 32 张证书。

## 编码

`Certificate.MarshalBinary` 使用自定义的标签-长度-值格式，所有字段均带
32 位长度前缀；`Signature` 覆盖除签名本身以外的全部字段（包括字段顺序和
集合中的重复项）。`ParseCertificate` 会拒绝未知、重复、截断或越界的字段。
