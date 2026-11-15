# ValidatingAdmissionPolicy参数缺失与失败放行路径

ValidatingAdmissionPolicy 的参数由绑定的 paramRef 指定，参数缺失时 parameterNotFoundAction 取 Allow 会让策略失效，failurePolicy 为 Ignore 时表达式出错也放行，攻击者删除参数对象或构造让表达式出错的对象就能绕过。为 Go 集群治理组件写供策略与绑定保存前调用的失效路径分析包：输入策略（failurePolicy、各校验表达式是否可能出错的静态标记、匹配条件）、绑定（paramRef 的 name 或 selector、parameterNotFoundAction、validationActions 含 Deny、Warn、Audit）以及参数资源现有对象与各主体的删除权限；报告请求被放行的条件：参数缺失且为 Allow、selector 命中零个对象且为 Allow、Ignore 下表达式出错、动作不含 Deny；paramRef 同时写 name 与 selector 拒绝；参数对象可被受限主体删除时标记为可绕过。用删除参数、零命中 selector、Ignore 出错、仅 Warn 和合规绑定验证。

## 包结构

- `types.go`：策略、绑定、参数对象、删除授权、发现项与报告类型。
- `failurepath.go`：`Analyze` 入口及四条放行路径、非法 paramRef、删除绕过分析。
- `selector.go`：`LabelSelector` 标签选择器匹配（In/NotIn/Exists/DoesNotExist）。
- `*_test.go`：删除参数、零命中 selector、Ignore 出错、仅 Warn、合规绑定等场景。

## 用法

```go
report := netsec.Analyze(policy, binding, paramObjects, deleteGrants)
switch {
case report.Rejected():
    // 存在 Blocker（如 name 与 selector 同时设置），拒绝保存。
case report.Bypassable():
    // 存在请求放行绕过路径，保存流程应拦截或要求整改。
default:
    // 未发现失效路径，允许保存。
}
```

发现项通过稳定的 `FindingCode` 区分（如 `ParameterMissingAllow`、`SelectorZeroMatchAllow`、
`IgnoreExpressionError`、`ValidationActionsWithoutDeny`、`ParameterDeletableByRestrictedSubject`），
`Finding.Bypassable` 标识是否构成放行绕过；Deny 下参数可删除仅为可用性风险，不标记绕过。
