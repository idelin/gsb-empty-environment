# 前端不收令牌

`response_type` 含 `token` 时，访问令牌会出现在重定向片段里，并可能被来源页读走。本包把这条安全规则实现为可导入的 Go 策略和状态机。

策略：

- 任何含 `token` 的 `response_type` 一律拒绝，即使客户端登记过该组合。
- 单独的 `code` 放行，响应参数放在 query 中。
- `id_token` 只能与 `code` 一起出现在已登记的 `code id_token` 混合模式中，响应参数放在 fragment 中。
- 单独的 `id_token`、未知响应类型、空值和重复项全部拒绝。
- 授权服务传入的 OAuth `state` 原样保留。
- 前端响应结构没有 access token 字段，无法把访问令牌交给前端。

```go
policy := netsec.NewPolicy("code id_token")

policy.Evaluate("code").Allowed            // true
policy.Evaluate("code token").Allowed      // false
policy.Evaluate("id_token").Allowed        // false
policy.Evaluate("code id_token").Allowed   // true

machine := netsec.NewStateMachine("code id_token")
result := machine.Apply(netsec.ServiceState{
	ResponseType: "code",
	State:        "opaque-state",
})
// result.Allowed == true, result.Phase == netsec.PhaseAuthorized,
// result.State == "opaque-state"
```

`NewFrontChannelResponse` 只为放行的决策构造前端响应；单独 `code` 只包含 code，混合模式必须同时提供 code 和 id_token，两种形式都不能携带 access token。
