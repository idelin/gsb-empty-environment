# TLS告警错误信息收敛

安全服务对不同握手失败返回不同长度错误，可能泄漏内部认证阶段。实现供Go接入宿主调用的错误封装层，内部保留详细类别，外部只按允许的公开类别映射固定结构；关闭、日志及回调均仅执行一次。用可计数传输测试并发失败、部分写入及敏感字段不会经错误包装或格式化回调泄漏。

## 设计

- **内部类别（不跨界）**：`NewInternalError` 保留握手阶段（版本协商、证书校验、PSK、授权等）、脱敏字段与底层原因；`Error()` 与 `Format`（含 `%+v`、`%#v`、宽度/精度动词）恒返回固定字符串，不实现 `Unwrap`，避免宿主沿包装链取内部信息。
- **公开类别（允许列表）**：`Publicize` 仅按 `publicMapping` 允许列表映射为四类固定宽度代码 `EHSK/EAUT/ESVC/EINT`，未登记阶段与任意外部错误一律收敛为 `EINT`。
- **固定结构**：`PublicError` 的文本、`MarshalJSON`（`{"code":...,"ref":...}`）与 `MarshalText` 线帧（`N` + 4 字符代码 + 32 字符关联 ID，共 37 字节）对所有类别等长，消除长度侧信道；关联 ID 用于关联本地脱敏日志。
- **单次语义**：`Guard` 对每条连接保证最多上报一次失败（重复调用返回 `ErrAlreadyReported`），日志与回调每次失败仅触发一次；`Close` 经 `sync.Once` 保证底层关闭只执行一次并复用其结果。
- **短写容忍**：`writeFull` 循环消费 `(n < len(p), nil)` 的部分写入，线帧最终完整重组；写入失败不抑制唯一一次日志与回调。
- **脱敏日志**：敏感值仅存于未导出字段，日志中统一替换为常量 `[REDACTED]`，字段名保留用于排障。

## 用法

```go
guard := netsec.NewGuard(conn,
    netsec.WithLogger(logger),
    netsec.WithFailureCallback(func(pub netsec.PublicError) {
        // 宿主侧只看到固定结构，可用 errors.Is 匹配哨兵错误
        switch {
        case errors.Is(pub, netsec.ErrAuthFailed):
            // ...
        }
    }),
)
defer guard.Close()

err := guard.Run(func() error {
    return netsec.NewInternalError(
        "stage:authorization", // 内部阶段标识，仅包内语义，不跨界
        "policy denied",
        underlyingErr,
        netsec.NewSensitiveField("psk", psk),
    )
})
```

## 测试

- `countingTransport`：可计数读/写/关闭调用与字节数，可配置每次写入上限（模拟部分写入）与写入失败阈值。
- 并发：64 个 goroutine 同时 `Report`，断言仅 1 次成功、1 条日志、1 次回调、线帧恰为 37 字节；32 个 goroutine 并发 `Close`，底层关闭仅 1 次。
- 泄漏扫描：对错误文本、全部 `fmt` 动词、JSON、线帧、回调入参、脱敏日志统一扫描密钥值（任何面均不得出现）与内部阶段名（仅允许出现在本地日志）。
- 运行：`go test -race -count=1 ./...`（当前覆盖率 94%）。
