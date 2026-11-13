# 图表渲染中的集群读取函数与秘密外泄

图表仓库里的第三方模板可以调用 lookup 去读集群里的 Secret，再把值写进 ConfigMap 或注解，渲染产物一旦被日志、缓存或审查页面展示，凭据就泄漏了。Go部署服务需要一个可导入的渲染准入包，输入模板中的函数调用列表（函数名、参数字面量）、渲染后的资源列表（kind、命名空间、name、各字符串字段值）和调用方给的集群 Secret 值查询回调：lookup 的 kind 参数为 Secret，或是动态拼接、无法静态确定时，一律拒绝；getHostByName、env、expandenv、readFile 这类读取外部状态的函数出现即拒绝；渲染后的非 Secret 资源里，任何字符串字段（含标签与注解的值）包含某个 Secret 值（长度不少于 6）的子串，或其 base64、十六进制形式的子串，都拒绝；拒绝时报告资源与字段路径。用 lookup Secret、动态 kind、ConfigMap 里的 base64 子串、十六进制写法和合法的 lookup Deployment 验证。

## 包 `netsec`

纯标准库实现，入口为 `Admit`：

```go
report, err := netsec.Admit(calls, resources, fetchSecrets, netsec.Options{})
if err != nil {
    // Secret 查询失败或缺少回调：fail-closed，拒绝本次渲染
}
if !report.Allowed {
    for _, v := range report.Violations {
        // v.Kind / v.FieldPath / v.Position / v.Encoding ...
    }
}
```

### 输入

- `FuncCall`：模板函数调用，参数用 `Arg{Value, Static}` 表示字面量或动态表达式。
- `Resource`：渲染资源，`Fields` 为点路径字符串字段（含 labels/annotations）；
  `NewResource(map[string]any)` 与 `Flatten` 可直接消费 `unstructured` 风格对象。
- `SecretFetcher`：调用方回调，返回集群中相关 Secret 的解码值。

### 策略

- `lookup` 的第 2 个参数 kind：字面量 `Secret`（大小写不敏感）拒绝；非字面量
  （变量、管道、拼接）或缺失导致无法静态确定，一律拒绝。
- `getHostByName`、`env`、`expandenv`、`readFile` 出现即拒绝，可通过
  `Options.ForbiddenFunctions` 追加。
- 非 Secret 资源的每个字符串字段，与长度 >= 6 的 Secret 值做子串匹配，覆盖明文、
  base64（Std/Raw/URL 变体）与十六进制（大小写）；字段整体可解码时再做一次
  解码后匹配，防止边界错位绕过。Secret 类型资源本身跳过。
- 命中按字段与编码去重，违规报告资源坐标、字段路径、Secret 坐标与编码形式。
- Secret 回调为 nil 或返回错误时直接返回 error，不放行。

### 验证

`go test ./...` 覆盖 lookup Secret、动态/缺失 kind、ConfigMap base64 子串、
十六进制大小写、合法 lookup Deployment/ConfigMap，以及禁用函数、明文注解、
短 Secret 豁免、Secret 资源跳过、回调 fail-closed 与对象拍平。
