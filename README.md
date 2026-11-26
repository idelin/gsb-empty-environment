# spiffefed

`spiffefed` 是一个仅依赖 Go 标准库、无网络 I/O 的 SPIFFE 联邦身份校验与工作负载证明策略包。它把 trust domain 当作硬隔离边界，所有验证路径都 fail closed。

## 安全不变量

- SPIFFE ID 只接受 `spiffe://<trust-domain>/<path>`。trust domain 仅允许 ASCII 小写字母、数字、`-`、`.`、`_`；路径中的 `.`、`..`、空段、百分号编码、查询参数和片段全部拒绝，不做规范化。
- X.509-SVID 必须恰好包含一个合法 SPIFFE URI SAN，不允许其他 SAN；拒绝 `cA=true`、非签名 KeyUsage、非 TLS ExtKeyUsage 和弱签名算法。
- X.509 验签只使用证书 URI SAN 中 trust domain 自己的 Trust Bundle。即使该域已被 `federates_with` 允许，也绝不会借用其他域的根证书。
- JWT-SVID 的 `sub` 必须是合法 SPIFFE ID；`aud` 必须精确命中本地预期受众；最大有效期为 24 小时，可配置为更短的值。
- JWT `kid` 只在 `sub` 所属 trust domain 的 JWKS 中查找，禁止全局搜索和跨域回退；嵌入 `jwk`/`jku`/`x5c` 等密钥头会被拒绝。
- 目标服务必须存在于联邦策略中；外部域还必须显式出现在该服务的 `federates_with` 列表。
- 工作负载注册的 node selector 与 process selector 集合必须双向全量精确相等；拒绝通配符、重复项、额外项、缺失项、命名空间选择器伪造和跨命名空间进程血缘。

## X.509-SVID

```go
bundle, err := spiffefed.NewTrustBundle("local.example", localRoot)
if err != nil {
	return err
}
partnerBundle, err := spiffefed.NewTrustBundle("partner.example", partnerRoot)
if err != nil {
	return err
}
bundles, err := spiffefed.NewBundleSet(bundle, partnerBundle)
if err != nil {
	return err
}

policy, err := spiffefed.NewFederationPolicy("local.example", map[string]spiffefed.ServicePolicy{
	"payments-api": {FederatesWith: []string{"partner.example"}},
	"internal":     {},
})
if err != nil {
	return err
}

verifier, err := spiffefed.NewX509SVIDVerifier(bundles, policy)
if err != nil {
	return err
}

id, err := verifier.Verify(peerLeaf, spiffefed.X509VerifyOptions{
	TargetService: "payments-api",
	Intermediates: peerIntermediates,
})
```

`peerIntermediates` 必须来自已经过认证的传输或证明流程，不能直接信任对端任意声明。验证器会按 `id.TrustDomain` 精确选择 bundle；缺失 bundle 直接失败。

## JWT-SVID

```go
localJWKS, err := spiffefed.ParseJWKS("local.example", localJWKSJSON)
if err != nil {
	return err
}
partnerJWKS, err := spiffefed.ParseJWKS("partner.example", partnerJWKSJSON)
if err != nil {
	return err
}
ring, err := spiffefed.NewJWTKeyRing(localJWKS, partnerJWKS)
if err != nil {
	return err
}

verifier, err := spiffefed.NewJWTSVIDVerifier(ring, "spiffe://local.example/payments-api")
if err != nil {
	return err
}
verifier.Policy = policy

svid, err := verifier.VerifyForService(token, "payments-api")
```

单域 key ring 会自动确定本地 trust domain。多域 key ring 应通过 `FederationPolicy` 或 `LocalTrustDomain` 明确本地域；未配置策略时外部域一律拒绝。

## 工作负载证明

```go
matcher, err := spiffefed.NewWorkloadMatcher([]spiffefed.WorkloadRegistration{{
	ID:               "payments-api",
	NodeSelectors:    []string{"node:region:cn-east", "node:role:worker"},
	ProcessSelectors: []string{"k8s:ns:payments", "k8s:sa:api", "unix:uid:1001"},
}})
if err != nil {
	return err
}

registration, err := matcher.Match(spiffefed.AttestationEvidence{
	Node: spiffefed.NodeAttestation{
		NodeID:    "node-1",
		Selectors: []string{"node:region:cn-east", "node:role:worker"},
	},
	Process: spiffefed.ProcessAttestation{
		ProcessRecord: spiffefed.ProcessRecord{
			PID:       100,
			ParentPID: 50,
			Namespace: "payments",
			UID:       1001,
		},
		Selectors: []string{"k8s:ns:payments", "k8s:sa:api", "unix:uid:1001"},
		Ancestors: []spiffefed.ProcessRecord{
			{PID: 50, ParentPID: 1, Namespace: "payments"},
			{PID: 1, ParentPID: 0, Namespace: "payments"},
		},
	},
})
```

进程祖先需按“直接父进程 → 命名空间根”排列。祖先与目标进程必须位于同一 namespace；`k8s:ns:*` 与 `unix:uid:*` 选择器还会与证明中的实际 namespace/UID 交叉核验。

匹配引擎假定 `AttestationEvidence` 来自受信 attestor；它负责注册策略与跨命名空间血缘的一致性校验，不替代内核/容器运行时侧的原始采集。

## 验证

测试使用真实 ECDSA P-256 密钥生成 CA、X.509-SVID 和 JWT-SVID，覆盖跨域伪造签发、多 URI SAN、路径规范化绕过、受众错配、跨域 `kid` 碰撞和选择器通配符/命名空间伪造：

```sh
go test ./...
go test -race ./...
```
