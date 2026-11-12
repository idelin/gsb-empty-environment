# GUAC图谱制品放行查询

## 策略包用法

包 `netsec` 暴露图快照类型与门禁入口 `Evaluate(artifactPurl, snapshot, policy)`，
返回 `Decision{Allowed, Reason, Detail, Cycles}`。

```go
decision := netsec.Evaluate(artifactPurl, netsec.Snapshot{
    Packages:     []netsec.PackageNode{...},
    Versions:     []netsec.VersionNode{{Purl: artifactPurl, Digests: map[string]string{"sha256": "..."}}},
    Attestations: []netsec.AttestationEdge{{
        VersionPurl:    artifactPurl,
        PredicateType:  "https://slsa.dev/provenance/v1",
        Attestor:       "https://accounts.google.com/expected-build-identity",
        SubjectDigests: map[string]string{"sha256": "..."},
    }},
    Dependencies: []netsec.DependencyEdge{{FromPurl: artifactPurl, ToPurl: depPurl}},
}, netsec.Policy{
    TrustedAttestors:       []string{"https://accounts.google.com/expected-build-identity"},
    VulnerabilityBlacklist: []netsec.BlacklistEntry{{Purl: "pkg:golang/github.com/acme/util"}},
})
```

## 判定规则

- **purl 规范化**（`NormalizePurl`）：type 与 namespace 小写；组件先百分号解码再统一
  大写十六进制重新编码；限定符键小写、重复键后值覆盖、按键排序输出；版本缺失直接报错。
  Go 包名大小写敏感，name 保留原样。所有图节点、依赖边与黑名单在比较前都先规范化，
  因此 `GOLANG/GITHUB.COM`、`%2f` 与 `%2F`、限定符顺序/键大小写等写法差异无法绕过。
- **SLSA 来源证明**：目标版本节点必须挂有 SLSA provenance 谓词（v0.1/v0.2/v1）的
  证明边，证明者身份与受信列表做**完全相等**匹配（拒绝 `trusted/evil`、同前缀域名等
  冒充），且证明主体摘要与版本节点摘要逐算法完全一致（同名不同来源、摘要不同的伪造
  证明拒绝）。
- **漏洞黑名单**：对根节点及整个传递依赖闭包做规范化 purl 匹配；带版本的条目只匹配
  该版本，无版本条目匹配该包所有版本。
- **依赖环**：DFS 三态标记（未访问/栈上/完成）保证环上不死循环，环记录在
  `Decision.Cycles` 中，黑名单判定在环图上仍然生效。

拒绝原因码：`INVALID_INPUT`、`VERSION_NOT_FOUND`、`MISSING_SLSA_PROVENANCE`、
`UNTRUSTED_ATTESTOR`、`SUBJECT_DIGEST_MISMATCH`、`VULNERABILITY_BLACKLIST_HIT`。

`go test ./...` 覆盖 purl 大小写/编码变形、证明者前缀冒充、摘要不一致证明、
传递依赖黑名单命中及依赖环终止。

GUAC 把 SBOM、SLSA 证明与漏洞数据汇成图，部署门禁按包标识查询图中的证明，攻击者可发布同名不同来源的包，往依赖图里塞伪造的证明节点，或借 purl 写法差异绕过黑名单。为 Go 发布门禁写供图查询结果判定调用的策略包：输入制品 purl 与图快照（包节点、版本节点、证明边含主体摘要、证明者身份、谓词类型、依赖边）；purl 比较前规范化：类型与命名空间小写，百分号编码统一，限定符按键排序且键小写，版本不可省略；放行须存在受信证明者对同一摘要的 SLSA 来源证明，证明者只认完全相同的主体而非前缀；依赖闭包中任一节点命中漏洞黑名单即拒绝，依赖环不得死循环。用 purl 大小写变形、证明者前缀冒充、摘要不同的证明和传递依赖命中验证。
