# 依赖版本范围漏洞交集

准入服务需要判断锁定版本是否落入复杂漏洞范围，预发布与补丁回移容易误判。写供Go服务调用的版本集合包，采用题内规则：三段非负整数加可选点分预发布标识，数字标识按整数、其他按字节排序，正式版高于预发布；支持区间并交差与精确回移豁免，输出规范化不相交区间和命中依据。用随机小版本全集核对集合运算和端点开闭，不依赖外部版本规范。

## 用法

```go
// 漏洞范围：[1.0.0, 1.4.0) ∪ [2.0.0-a, 2.0.0]
vuln := netsec.ClosedOpen(netsec.MustParse("1.0.0"), netsec.MustParse("1.4.0")).
    Union(netsec.Closed(netsec.MustParse("2.0.0-a"), netsec.MustParse("2.0.0")))

policy := netsec.Policy{
    Vulnerable: vuln,
    // 精确回移豁免：1.2.3 已回合补丁
    Exemptions: netsec.ExactVersions(netsec.MustParse("1.2.3")),
}

verdict := policy.Check(netsec.MustParse("1.2.3"))
// verdict.Exempted == true，verdict.Reason 给出命中依据
```

- `Set` 支持 `Union` / `Intersect` / `Difference` / `Exempt`，`String()` 输出规范化不相交区间。
- `Set.Match` 与 `Policy.Check` 返回命中区间与原因，作为准入判定依据。
- 区间端点各自独立开闭，可用 `Closed` / `Open` / `ClosedOpen` / `OpenClosed` / `AtLeast` / `LessThan` 等构造。
