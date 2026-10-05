# delegation — 授权链校验库

Go 实现的委托图（delegation graph）校验库，支持多级继续委托、
时点查询与最短失效原因链输出。

## 规则

- **继续委托**：任何授权都可以作为父授权继续向下签发子授权。
- **包含约束**：子授权的动作集合、资源集合必须分别是父授权的子集，
  有效期必须完全落在父授权的有效期内。
- **撤销传播**：撤销上游授权后，其全部后代对撤销时刻及之后的查询立即失效。
- **历史准确**：签发、撤销、取代都带时间戳，任意历史时点的查询结果不变。
- **同名重发**：同一 ID 重复签发会产生递增的代次（generation），
  新一代在签发时刻取代旧一代；未指定代次的父引用绑定到
  子授权签发时刻的最新一代。

## 用法

```go
s := delegation.NewStore()

// 根授权（无父引用）
s.Issue(delegation.Grant{
    ID: "root", Issuer: "ca", Subject: "team",
    Actions: []string{"read", "write"}, Resources: []string{"repo/*"},
    NotBefore: t0, NotAfter: t100,
}, t0)

// 子授权：范围必须不超出父授权
s.Issue(delegation.Grant{
    ID: "ci", Issuer: "team", Subject: "ci-bot",
    Actions: []string{"read"}, Resources: []string{"repo/*"},
    NotBefore: t10, NotAfter: t90,
    Parent: &delegation.Ref{ID: "root"}, // Generation 0 = 签发时刻最新一代
}, t10)

// 时点校验
res := s.Validate("ci", 0, t50)
if !res.Valid {
    // res.Reason: REVOKED / EXPIRED / SUPERSEDED / PARENT_MISSING / ...
    // res.Chain:  最短失效原因链，如 ["ci#1", "root#2"]
}

// 撤销上游：后代立即失效；t50 之前的历史查询仍返回有效
s.Revoke("root", 0, t60)
```

## 失效原因

| Reason | 含义 |
| --- | --- |
| `NOT_FOUND` | 授权（或指定代次）不存在 |
| `NOT_YET_ISSUED` | 查询时点早于签发时刻 |
| `NOT_YET_VALID` | 查询时点早于 NotBefore |
| `EXPIRED` | 查询时点不早于 NotAfter |
| `REVOKED` | 该代次已被撤销 |
| `SUPERSEDED` | 该代次已被同名重发取代 |
| `PARENT_MISSING` | 父链缺失（父授权或指定代次不存在） |
| `ACTIONS_EXCEED_PARENT` | 动作集合超出父授权 |
| `RESOURCES_EXCEED_PARENT` | 资源集合超出父授权 |
| `VALIDITY_EXCEEDS_PARENT` | 有效期超出父授权 |
| `CYCLE` | 父链成环 |

## 测试

```sh
go test ./...
```
