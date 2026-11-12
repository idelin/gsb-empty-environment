# age插件名与响应约束

恶意或被替换的 age 插件想返回其他接收者类型的 stanza，或借插件名读取任意可执行文件。为Go加密宿主写可导入的 age 插件会话包，通过调用方提供的字节流接口交互：插件名取自 AGE-PLUGIN-<名>-1 前缀，只许小写字母、数字与连字符，映射为 age-plugin-<名>；wrap-file-key 返回的 stanza 首参数须等于该接收者声明的类型，单次响应不超过64KiB，文件密钥须为16字节；未知命令按协议忽略。测名称注入、类型不符、超长。

## 实现

纯标准库实现，宿主本身不启动进程、不访问文件系统，只通过调用方提供的
`io.Reader` / `io.Writer` 与插件通信。

- 根包 `netsec`（`plugin.go`、`stanza.go`）：完整会话实现。
- 子包 `ageplugin`：同实现的别名再导出，供 `import .../ageplugin` 使用。

### 安全边界

- `ParsePluginName` 只接受 `AGE-PLUGIN-<名>-1`，中段转小写后必须匹配
  `^[a-z0-9-]+$`，因此不可能出现 `/`、`.`、空格、`;`、NUL 等字符，
  从根上杜绝借插件名拼接任意可执行路径（`BinaryName` 再做一次同样校验）。
- `Session.WrapFileKey` 校验每个 `recipient-stanza` 的首参数严格等于
  本会话声明的接收者类型，不符返回 `ErrRecipientType`。
- 每条插件响应独立计费，按线路原始字节精确限制在 `MaxPluginResponse`
  （64KiB），超限返回 `ErrResponseTooLarge`；预算在命令之间重置。
- 文件密钥必须为 `FileKeySize`（16）字节，否则返回 `ErrFileKeySize`，
  且不会向插件发送任何字节。
- 握手与响应中的未知 stanza/命令一律忽略，仅识别 `greet`、
  `recipient-stanza`、`done`、`error`。

### 用法

```go
session, err := netsec.NewSession(pluginStdout, pluginStdin, "AGE-PLUGIN-yubikey-1")
if err != nil { /* 名称非法等 */ }

executable := session.BinaryName() // "age-plugin-yubikey"，由调用方负责启动
session.AddRecipient([]string{"AGE-PLUGIN-yubikey-1", "age1..."})
stanzas, err := session.WrapFileKey(fileKey) // fileKey 必须为 16 字节
```

### 测试

`go test ./... -race` 覆盖名称注入（路径穿越、分隔符、空名、错误前后缀等）、
类型不符（含缺失首参数）、超长（64KiB 精确边界、超 1/64/4096 字节、
跨响应预算重置）、16 字节密钥、未知命令忽略、协议握手与畸形报文。
