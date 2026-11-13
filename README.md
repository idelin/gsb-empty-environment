# Cedar禁令求值出错放行

Cedar 风格的策略引擎里 forbid 优先于 permit，但某条 forbid 在求值时因属性缺失而出错会被跳过，攻击者故意省略上下文属性，让禁令失效。为Go授权服务写可导入的策略评估包，供请求授权调用，只处理本题子集：permit 与 forbid 的 (principal, action, resource) 加 when 条件，条件支持 ==、!=、<、>、&&、||、has、属性访问 a.b 与 in；默认拒绝；任一 forbid 满足即拒绝；求值出错（访问不存在属性、类型不符）的策略视为不适用并记入诊断，但出错的是 forbid 且调用方设置了 failClosed 时整个请求拒绝；用 has 保护的属性访问不出错；未设置 failClosed 时，出错的 forbid 被跳过，permit 仍须有求值不出错且满足的策略才放行；输出决定及每条策略的状态（满足、不满足、出错）。用缺属性的 forbid 被跳过、failClosed、has 保护、类型不符和 permit 出错验证。

## 导入与调用

模块路径为 `example.org/netsec/q0730`，包名 `netsec`，仅依赖标准库。

```go
import "example.org/netsec/q0730"

policies, err := netsec.ParsePolicies(`
    forbid principal action resource
        when principal has "clearance_level" &&
             principal.clearance_level < resource.required_level;
    permit principal action resource
        when action.name == "read";
`)
if err != nil {
    log.Fatal(err)
}

result := netsec.Evaluate(policies, &netsec.Request{
    Principal: map[string]any{"name": "mallory"}, // 故意省略 clearance_level
    Action:    map[string]any{"name": "read"},
    Resource:  map[string]any{"required_level": 3.0},
}, true) // true = failClosed

// result.Decision: allow | deny
// result.Diagnostics: 每条策略一条记录，Status 为 matched | not_matched | error
```

也可以不经过文本解析，直接用 `ast.go` 中的 `Policy` 与 `Expr` 类型构造策略。

## 策略语法

```
permit|forbid principal action resource [when <expr>] ;
```

表达式优先级（从低到高）：`||` < `&&` < `== != < > <= >=` < `has`/`in`/属性访问 `.` < 括号与字面量。
`&&`、`||` 短路求值；集合字面量形如 `["read", "view"]`，作为 `in` 的右侧。

## 安全语义要点

- 默认拒绝；任一 `forbid` 条件为真即拒绝，且 forbid 优先于 permit。
- 求值出错（缺属性、在非记录上取属性、类型不符）的策略记为 `error` 并视为不适用。
- 出错策略是 `forbid` 且 `failClosed=true`：整个请求拒绝（推荐在对外服务中开启）。
- `failClosed=false`：出错 forbid 被跳过，但只有求值成功且为真的 permit 才能放行。
- `has` 是受保护的存在性判断，配合 `&&` 短路可安全访问可选属性，缺属性不出错。
- `==`/`!=` 两侧类型不同按不相等处理，不属于求值错误；`<`、`in`、`&&`、`||` 的类型不符属于错误。

## 验证

`go test ./...` 覆盖：缺属性 forbid 被跳过、failClosed 拒绝、has 保护、
类型不符、permit 出错、forbid 优先级、in/比较/跨类型相等及默认拒绝。
