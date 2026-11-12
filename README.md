# ManageSieve脚本上传准入

用户通过 ManageSieve 上传的 Sieve 脚本由服务器代为执行，攻击者可在脚本名里夹带换行或引号改写命令，用字面量长度声明偷渡第二条命令，或 require 站点未开放的扩展以执行外部程序。给Go ManageSieve 服务写供 PUTSCRIPT 处理调用的校验包：脚本名按 UTF-8 不超过 128 字节且不含控制字符；脚本体为 {n+} 或 {n} 字面量时精确读取 n 字节，其后多余字节不得当作下一条命令解释；require 必须是脚本的第一条命令，所列扩展须全在站点白名单内；redirect 每脚本不超过调用方给的次数；任一项失败都不得保存也不得覆盖当前脚本。用名称里的 CRLF、字面量后紧跟 DELETESCRIPT、未开放扩展、require 非首条验证。

## 包结构

- `name.go`：`ValidateScriptName`，名称按 UTF-8 不超过 128 字节、非空且不含控制字符（含 CR/LF/TAB）。
- `protocol.go`：`ReadPutScript`，解析 `PUTSCRIPT` 命令行；`{n}`/`{n+}` 字面量严格读取 n 字节，结束符后若仍有字节（如偷渡的 `DELETESCRIPT`）返回 `ErrTrailingData`，绝不解释为下一条命令。
- `sieve.go`：`ValidateScript`，Sieve 词法分析后校验 `require` 必须为首条命令、扩展全部命中白名单、`redirect` 数量不超过上限；字符串与多行 `text:` 文本中的字样不会被当成命令。
- `store.go`：`ScriptStore` 接口、内存实现 `MemoryStore` 与一站式入口 `HandlePutScript`；只有协议解析和内容校验都成功才调用 `Put`，任何失败都保留原脚本不变。

## 用法

```go
rules := netsec.Rules{
    AllowedExtensions: []string{"fileinto", "reject"},
    MaxRedirects:      1,
}
store := netsec.NewMemoryStore()
// conn 是已认证的 ManageSieve 连接。
if _, err := netsec.HandlePutScript(bufio.NewReader(conn), store, rules); err != nil {
    // 返回 NO；出现 ErrTrailingData 时还应关闭连接。
}
```

## 失败语义

所有拒绝路径都在任何写入之前返回哨兵错误（见 `errors.go`），可通过 `errors.Is` 判定。遇到 `ErrTrailingData` 表示连接上已夹带走私命令，调用方应当关闭连接而不是继续读取下一条命令。
