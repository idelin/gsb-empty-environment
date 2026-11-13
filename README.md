# 凭据助手协议换行注入

Git 托管平台的凭据助手按 `key=value` 逐行读写，攻击者在仓库地址的用户名或路径里塞 `%0a`，借换行注入 `host=` 等字段，把令牌发给自己的主机（CVE-2020-5260 / CVE-2020-11008 同类问题）。

`netsec` 包提供可导入的凭据助手请求构造与响应解析封装，集中实施三条安全不变量：

- URL 拆出的 `protocol`、`host`、`path`、`username` 在百分号解码后只要含 LF（`%0a`）、CR（`%0d`）或 NUL（`%00`，含裸字节）即整体拒绝，返回 `ErrUnsafeValue`，不产生任何请求。
- 请求里的 `host` 固定为 URL 的真实主机（含端口 / IPv6 括号），`Request.Encode` / `WriteTo` 序列化前再次校验，杜绝追加注入行。
- 解析助手响应时，未被请求的 `protocol` 与 `host` 字段一律忽略；被请求的字段若与请求不一致则直接报错，凭据结果中的 `Protocol`/`Host` 永远来自可信请求。

## 用法

```go
import "example.org/netsec/q0842"

req, err := netsec.NewRequest("https://alice@github.com/acme/repo.git")
if err != nil {
    // 注入或非法 URL：拒绝整个请求
    return err
}

wire, err := req.Encode() // protocol=https\nhost=github.com\n...
if err != nil {
    return err
}

// 将 wire 发给助手进程，再解析其 stdout：
cred, err := netsec.ParseResponse(req, helperStdout)
if err != nil {
    return err // 主机不匹配、畸形行或控制字符
}
// cred.Host 始终等于 req.Host（github.com），不会被助手响应改写
_ = cred.Password
```

## API

- `NewRequest(rawURL string) (Request, error)`：安全解析仓库 URL，校验全部解码字段。
- `Request.Encode() ([]byte, error)` / `Request.WriteTo(io.Writer)`：输出 `key=value` + 空行结尾的助手报文。
- `Request.Validate() error`：校验报文字段不含 LF、CR、NUL。
- `ParseResponse(req Request, r io.Reader) (Credential, error)`：按请求字段白名单解析助手响应。

## 测试

```sh
go test -race -cover ./...
```
