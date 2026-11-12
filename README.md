# GitHub环境保护主体重拼

环境保护规则只对声明了 environment 的作业生效，攻击者让作业不声明环境，或在环境名里放冒号，凑出与受保护环境相同的 sub 去换发布角色。为Go凭据换发服务实现可导入的角色条件判定包：对已验签的 claims，用 repository 与 environment 重新拼出 repo:<仓库>:environment:<环境>，与 sub 逐字相等才算环境身份；environment 须在角色允许集合内且不含冒号与 *；缺 environment、sub 为 ref: 或 pull_request 形态、event_name 为 pull_request_target 一律拒绝；runner_environment 为 self-hosted 时拒绝标记为仅托管的角色。用冒号环境名、分支触发、缺字段与自托管 runner 验证。

## 使用

```go
role := netsec.Role{
	ID:                  "release",
	AllowedEnvironments: []string{"production"},
	HostedOnly:          true,
}

if err := netsec.Authorize(claims, role); err != nil {
	// 拒绝；具体原因可通过 errors.Is(err, netsec.ErrInvalidEnvironment) 等判断。
}
```

`Authorize` 返回 `nil` 表示通过。`Evaluate` 返回包含稳定 `Reason` 的
`Decision`；`CheckRole` / `CanAssumeRole` 返回布尔值。claims 可以是
`Claims`、`map[string]any`（常见 JWT claims 形式）或 JSON 字节。
