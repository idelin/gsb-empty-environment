# frame-ancestors与X-Frame-Options并存判定

页面同时带 X-Frame-Options 与 CSP frame-ancestors 时，不同浏览器的取舍不同，攻击者借重复头或畸形来源表达式让页面可被嵌入，实施点击劫持。为Go网关写可导入的嵌入策略判定包，供响应出口调用：存在 frame-ancestors 时忽略 X-Frame-Options；frame-ancestors 的来源列表支持 'none'、'self'、方案、主机（可带 *. 通配前缀与端口），'none' 与其他来源共存时 'none' 无效、其他来源仍生效；同名 CSP 头出现多次时取全部策略的交集；Report-Only 策略不参与判定；X-Frame-Options 只认 DENY 与 SAMEORIGIN，多个值（含逗号）或重复头视为无效，本题规定按拒绝嵌入处理；嵌套祖先链上每一层祖先都须匹配，不只检查直接父页面。用 X-Frame-Options 被忽略、'none' 共存、Report-Only、重复 X-Frame-Options 和三层嵌套的中间层验证。

## 用法

包路径为 `example.org/netsec/q0556`，在响应出口调用 `Evaluate`：

```go
import netsec "example.org/netsec/q0556"

// resource 为被嵌入页面自身 URL；ancestors 从最外层祖先排到直接父页面。
decision := netsec.Evaluate(resp.Header, resourceURL, ancestorURLs)
if !decision.Allowed {
    // 拒绝嵌入（例如剥离 frame、替换为拦截页），decision.Mechanism/Reason 可审计
}
```

只需布尔结论时使用 `netsec.Allowed(...)`；`ancestors` 为空表示顶层加载，一律放行。

## 判定语义

- `Content-Security-Policy` 中存在生效的 `frame-ancestors` 时，`X-Frame-Options` 被完全忽略；`Content-Security-Policy-Report-Only` 不参与判定。
- 多条强制 CSP 的 `frame-ancestors` 取交集：每条策略内部来源为 OR，祖先链每一层都必须被所有策略放行。
- 来源支持 `'none'`、`'self'`、纯方案（`https:`）、主机来源（`https://shop.example:8443`、`https://*.shop.example`、`https://shop.example:*`）；`'none'` 与其他来源共存时失效；无法解析的畸形 token 按 CSP 规则忽略，整条指令无有效来源时不产生限制。
- `X-Frame-Options` 仅接受恰好一个 `DENY` 或 `SAMEORIGIN`（大小写不敏感）；重复头、逗号多值、空值或未知指令（如 `ALLOW-FROM`）一律拒绝嵌入。
- 三层及以上嵌套时逐层校验，中间层不匹配同样拒绝。

## 验证

`go test ./...` 覆盖题目点名的五个场景：X-Frame-Options 被忽略、`'none'` 与其他来源共存、Report-Only 不生效、重复 X-Frame-Options 拒绝、三层嵌套中间层拦截，另含多 CSP 交集与来源表达式边界用例。
