# 安全邮件头构造组件

`example.org/netsec/q0312` 提供可供 Go 邮件发送服务导入的 RFC 5322 头与 MIME 正文构造器。调用方只能通过类型化 API 提供数据，不能直接写原始头、指定顶层 `Content-Type` 或指定 MIME 边界。

## 使用

```go
from, err := netsec.NewAddress("张三", "sender@example.com")
to, err := netsec.NewAddress("Alice", "alice@example.com")

msg := netsec.NewMessage()
if err := msg.SetFrom(from); err != nil { /* ... */ }
if err := msg.SetTo(to, "bob@example.com"); err != nil { /* ... */ }
if err := msg.SetSubject("季度报告"); err != nil { /* ... */ }
if err := msg.SetTextBody("报告见附件。\n"); err != nil { /* ... */ }
if err := msg.AddAttachment("报告 2026.pdf", "application/pdf", pdf); err != nil { /* ... */ }

wire, err := msg.Build()

// SMTP 信封与头部收件人分开校验。
envelope, err := netsec.NewEnvelope(
	"sender@example.com",
	[]string{"alice@example.com", "bob@example.com"},
)
rcptTo, err := envelope.RcptTo()
```

## 安全边界

- 显示名按原始 UTF-8 输入，必要时编码为不超过 75 字符的 RFC 2047 B-encoded-word，并按 RFC 5322 折行。CR、LF、裸控制字符、非法 UTF-8 和预编码的 encoded-word 会被拒绝。
- 邮箱地址必须是单个 ASCII `addr-spec`；信封地址额外拒绝显示名、列表、注释和所有非 ASCII 内容。
- 每个输出字段由唯一方法写入：`From`、`To`、`Cc`、`Bcc`、`Reply-To`、`Subject`、`Date`、`Message-ID`、`MIME-Version`、`Content-Type` 和 `Content-Transfer-Encoding`。不存在原始头写入入口。
- 顶层内容类型由正文/附件状态生成；MIME 边界由 `crypto/rand` 生成，且会检测正文碰撞后重新生成。附件 `Content-Type` 只接受无参数的裸媒体类型，嵌套 `multipart/*` 被拒绝。
- 附件文件名同时输出 ASCII 回退值和 RFC 2231 `filename*=UTF-8''...` 参数；超长值使用带编号的参数续行，百分号转义不会在 `%XX` 中间截断。
- `SetBcc` 只用于需要生成 Bcc 头的明确场景。需要隐藏密送收件人时，发送服务应在传输前移除该头，并把信封 `RcptTo` 作为唯一投递列表。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试覆盖换行头注入、预编码/百分号双重编码、超长显示名折行、伪造边界与参数、合法多收件人及信封校验隔离。
