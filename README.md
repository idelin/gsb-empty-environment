# MySQL本地文件请求应答绑定

恶意MySQL服务器可在查询应答里发0xFB本地文件请求，让客户端上传本机文件。给Go驱动写供应答读取循环调用的准入包：仅当客户端刚发出LOAD DATA LOCAL语句且请求的文件名与语句一致时才读取，其余0xFB拒绝并回空包；AuthSwitchRequest(0xFE)要求mysql_clear_password时，仅在调用方开启且通道为TLS时响应。用中途0xFB、改路径和明文切换验证。

## 接入方式

每条连接持有一个 `netsec.Guard`，在两个位置接入即可：

1. 发送 COM_QUERY 后调用 `guard.NoteQuery(query)`，只有形如
   `LOAD DATA [LOW_PRIORITY|CONCURRENT] LOCAL INFILE '<file>'` 的语句会被记录，
   其它语句会解除待处理状态。
2. 应答读取循环遇到特殊首字节时交给 Guard 判定：
   - `0xFB`：调用 `AdmitLocalInfile(payload, conn)`。返回错误时 Guard 已经写好
     空终止包，驱动直接跳过文件传输；成功时返回已打开的文件句柄，由驱动流式
     回传并补空包结束。
   - `0xFE`：调用 `AdmitAuthSwitch(payload)`。仅当插件为 `mysql_clear_password`、
     调用方显式开启明文密码且连接为 TLS 时放行；其余情况返回
     `ErrCleartextDenied`，驱动不得回传密码。其它认证插件不受此策略限制。

策略默认 fail-closed：畸形包、空文件名、路径与语句不一致、重复 0xFB 一律拒绝。

## 验证

```sh
go test ./... -count=1 -race
```

`admission_test.go` 覆盖三类攻击向量：应答中途注入的 0xFB、把请求文件名改成
敏感路径、以及明文 `AuthSwitchRequest` 在无 TLS/未开启时的拒绝，并通过一个脚本化
伪连接的读取循环测试串起完整交互。
