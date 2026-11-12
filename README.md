# ValidatingAdmissionPolicy参数缺失与失败放行路径

ValidatingAdmissionPolicy 的参数由绑定的 paramRef 指定，参数缺失时 parameterNotFoundAction 取 Allow 会让策略失效，failurePolicy 为 Ignore 时表达式出错也放行，攻击者删除参数对象或构造让表达式出错的对象就能绕过。为 Go 集群治理组件写供策略与绑定保存前调用的失效路径分析包：输入策略（failurePolicy、各校验表达式是否可能出错的静态标记、匹配条件）、绑定（paramRef 的 name 或 selector、parameterNotFoundAction、validationActions 含 Deny、Warn、Audit）以及参数资源现有对象与各主体的删除权限；报告请求被放行的条件：参数缺失且为 Allow、selector 命中零个对象且为 Allow、Ignore 下表达式出错、动作不含 Deny；paramRef 同时写 name 与 selector 拒绝；参数对象可被受限主体删除时标记为可绕过。用删除参数、零命中 selector、Ignore 出错、仅 Warn 和合规绑定验证。

## 用法

```go
report := netsec.Analyze(policy, binding, params, subjects)
if report.Rejected {
    // 拒绝保存：paramRef 同时设置了 name 与 selector。
}
if report.Allowed {
    // 至少存在一条放行路径，应阻止保存或提示治理人员。
}
if report.Bypassable {
    // 受限主体可以删除参数对象，删除后参数缺失会触发 Allow。
}
for _, finding := range report.Findings {
    // finding.Code 标明具体路径，例如
    // netsec.FindingParameterMissingAllowed。
}
```

`AnalyzeBindings` 接受同一策略的全部绑定，并在每条 `Finding` 中通过
`BindingName` 与 `BindingIndex` 标明来源绑定。表达式不会被实际执行；调用方需
在保存前完成静态检查，并用 `Validation.MayError` / `MatchCondition.MayError`
传入“可能出错”的结论。

`Subject.Restricted` 由调用方根据集群权限模型标记；`CanDeleteParams` 支持具体
参数对象名和 `"*"`。删除权限只有在 `parameterNotFoundAction` 为 `Allow` 时才会
构成参数缺失放行路径，因此 `Bypassable` 也只在此时置位。
