# OP_MSG认证前命令门禁

未认证连接想借 OP_MSG 的多段文档或未知标志位运行受限命令。为Go代理写可导入的 OP_MSG 认证前门禁：flagBits 低16位出现未识别位即拒，checksumPresent 时校验 CRC-32C；恰有一个 kind 0 段，命令名取其文档第一个键；未认证只放行 hello、isMaster、saslStart、saslContinue，且 $db 须是字符串 admin；kind 1 段长度不得越界。测多段夹带、错长度、坏校验和、命令大小写。

## 用法

```go
import "example.org/netsec/q0616"

// frame 必须是完整的 OP_MSG 线缆消息（含 messageLength 头）。
if err := netsec.CheckPreAuthOPMsg(frame); err != nil {
	// 拒绝并在认证前断开或返回认证错误。
}
```

需要读取已通过校验的命令名和数据库时，使用：

```go
msg, err := netsec.InspectPreAuthOPMsg(frame)
if err != nil {
	// 拒绝
}
// msg.Command 为 kind 0 文档的第一个键，msg.Database 为 $db。
```

## 门禁规则

- `flagBits` 低 16 位只接受 `checksumPresent`、`moreToCome`、`exhaustAllowed`，其余位拒绝；高 16 位按协议忽略。
- `checksumPresent` 时校验消息末尾 CRC-32C，校验范围不包含校验和字段本身。
- 消息必须包含且只包含一个 kind 0 段；kind 1 段可在其之前，但 kind 0 必须是最后一个段。
- kind 1 的 `size` 包含自身 4 字节但不包含 kind 字节，且不得越过消息（或越过校验和）边界；段内 BSON 文档必须完整。
- 命令名严格取 kind 0 文档的第一个键，区分大小写，仅放行 `hello`、`isMaster`、`saslStart`、`saslContinue`。
- kind 0 顶层字段名必须唯一；`$db` 必须存在、为 BSON string 且值严格等于 `admin`。
