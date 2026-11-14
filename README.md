# JSONP回调名被拼进了脚本

旧接口支持`callback`参数返回脚本，攻击者在回调名里注入表达式或闭合符号，在受害站点上下文执行任意代码。请实现供Go兼容接口调用的可导入JSONP响应组件。回调名必须是单段标识符并匹配声明字符集与长度，编码后的形式也要规范化后校验；响应内容类型固定为脚本、加nosniff，且只允许同源或白名单来源调用。敏感接口禁止JSONP，凭据不随脚本请求发送。用表达式注入、闭合符、Unicode转义、超长回调、合法回调和白名单来源验证只返回合法包装。

## 使用方式

根包 `netsec` 和可导入子包 `jsonp` 提供同一套 API：

```go
handler := netsec.NewHandler(netsec.Options{
    AllowedOrigins: []string{"https://app.example.com"},
    Data:           map[string]any{"ok": true},
})
http.Handle("/public/jsonp", handler)
```

也可以在每次请求时传入数据：

```go
err := netsec.WriteJSONP(w, r, payload, netsec.Options{
    AllowedOrigins: []string{"https://app.example.com"},
})
```

子包导入方式：

```go
import "example.org/netsec/q0303/jsonp"

handler := jsonp.NewHandler(jsonp.Options{
    AllowedOrigins: []string{"https://app.example.com"},
    Data:           payload,
})
```

## 安全约束

- `callback` 只接受单段 ASCII JavaScript 标识符 `[A-Za-z_$][A-Za-z0-9_$]*`，默认最长 64 字节，并拒绝保留字。点号、括号、引号、反斜杠、空白、控制字符、`\uXXXX` 转义全部拒绝。
- 查询参数先由 `net/url` 解码；组件继续规范化多层百分号编码，再按上述字符集、长度和保留字规则校验。重复或歧义的同名参数直接拒绝。
- 成功响应固定为 `application/javascript; charset=utf-8`，并设置 `X-Content-Type-Options: nosniff`、`Cache-Control: no-store` 和 `Vary: Origin, Referer`。
- 只允许同源 `Origin`/`Referer`，或 `AllowedOrigins` 白名单中的完整来源。`Origin: null`、多来源、后缀混淆、协议不一致和缺失来源信息均拒绝。
- `Sensitive`、`DisableJSONP`、`SensitivePaths` 或 `SensitiveFunc` 命中的接口不返回任何 JSONP 包装。
- 默认拒绝带 `Cookie`、`Authorization`、`Proxy-Authorization` 的请求，响应不会写入 `Set-Cookie` 或 `Access-Control-Allow-Credentials`。
- JSON 序列化后会再次转义 `<`、`>`、`&`、U+2028 和 U+2029，自定义 `MarshalFunc` 的输出也必须通过 JSON 校验。

运行验证：

```sh
go test ./...
```
