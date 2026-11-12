# rsync daemon 请求准入

包 `example.org/netsec/q0302`（Go 包名 `netsec`）为 Go 的 rsync 守护服务提供可导入的请求准入检查。它按模块评估：

- `hosts deny` 优先于 `hosts allow`；两者都未命中且 `hosts allow` 非空时拒绝。
- 模块名精确、区分大小写匹配。
- 请求路径必须以模块根为基准规范化；绝对路径、`..` 越界、反斜杠和 NUL 字节拒绝。
- `use chroot = no` 时，逐组件检查符号链接，任何指向模块根外的链接都拒绝。
- `refuse options` 同时覆盖长选项、rsync 短选项、选项簇和 `--no-` 形式。
- `exclude` 使用 rsync 风格通配（`*`、`?`、`**`、字符类），并支持 `!` 重新包含。
- `read only = yes` 的模块只拒绝上传/写入，读取仍允许。

```go
cfg, err := netsec.ParseConfigFile("/etc/rsyncd.conf")
if err != nil {
    return err
}

request := netsec.Request{
    Module:  "files",
    Host:    "10.1.2.3",
    Path:    "reports/2026/summary.txt",
    Options: []string{"-r", "--delete"},
    Upload:  false,
}
if err := netsec.Check(*cfg, request); err != nil {
    // err is a *netsec.DeniedError with Reason and Detail.
    return err
}
```

也可以用 `Config{Modules: map[string]Module{...}}` 或单个 `Module` 的 `Evaluate`
方法直接构建策略，并用 `Evaluate` / `EvaluateRequest` 获取带拒绝原因的
`Decision`。`Check`、`CheckRequest`、`Admit` 和 `Authorize` 返回 `error`。
