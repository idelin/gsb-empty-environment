# 行式审计日志安全编码组件

`example.org/netsec/q0482` 提供一个可直接导入 Go 审计服务的行式日志编码器。用户输入只作为 `fields` 中的数据处理，不能直接影响信封字段或另起一行。

## 记录格式

每条记录是一行确定性 JSON（JSONL），字段顺序和转义规则固定：

```json
{"v":1,"seq":1,"timestamp":"2026-11-14T12:34:56.123456789Z","subject":"svc-42","event_id":"...","fields":{"action":"approve","comment":"..."},"prev":"0000...0000","hash":"..."}
```

- `timestamp`、`subject`、`event_id`、`seq`、`prev`、`hash` 由服务端生成或维护；用户字段中的同名键只会保存在 `fields` 内，无法覆盖信封。
- 用户字段中的换行、回车、制表符、NUL、C0/C1 控制字符、Unicode 方向/格式控制字符、U+2028/U+2029、双引号、反斜杠等都会转义，因此一条用户事件始终只占一行。
- `hash` 覆盖版本、序号、时间、主体、事件 ID、字段和上一条摘要；`prev` 指向上一条记录的 `hash`。默认使用 SHA-256，配置 `WithKey` 后使用 HMAC-SHA256。

## 使用

```go
var log bytes.Buffer
key := []byte("audit-chain-key-from-kms")

enc, err := netsec.NewEncoder(
	&log,
	netsec.WithSubject("svc-42"), // 来自已认证的服务端上下文
	netsec.WithKey(key),
)
if err != nil {
	return err
}

// fields 可以包含任意用户输入；无需手工过滤换行或分隔符。
if err := enc.Write(map[string]string{
	"action":  "approve",
	"comment": userComment,
}); err != nil {
	return err
}

sealed, err := enc.Seal() // 刷新目标并固定记录数与链头
if err != nil {
	return err
}

v, err := netsec.VerifySealed(bytes.NewReader(log.Bytes()), sealed, netsec.WithKey(key))
if err != nil {
	return err // 记录被改写、插入、删除、重排或截断
}
_ = v // v.Records、v.Head、首末序号和时间
```

写入到文件时，用 `VerifyFile(path, opts...)` 或打开读句柄后调用 `Verify`。若目标可回读，`enc.Verify()` 会把回读结果与编码器内存中的记录数和链头比对；`enc.SealAndVerify()` 可一次完成密封、计数和链校验。

## 校验语义

`Verify` 拒绝空行、CRLF、非确定性 JSON、重复键、未知键、缺失信封字段、非 RFC3339Nano UTC 时间、重复事件 ID、序号断裂、`prev` 不匹配和摘要不匹配。`VerifyCount` 额外要求精确记录数；`VerifySealed` 同时校验密封的记录数与链头，因此合法链前缀式的截断也会被发现。

普通 SHA-256 链可以检测意外修改和未重算链头的篡改；如果攻击者可能直接改写日志文件并重算整条链，应使用 `WithKey` 的 HMAC 链，密钥只保存在审计服务/KMS 中。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖换行与控制字符注入、伪造时间戳与信封字段、篡改字段、插入伪造审批记录、合法多记录日志、摘要链、密钥链重算攻击和截断计数。
