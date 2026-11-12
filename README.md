# 纵向遥测随机化记忆

终端隐私遥测需要对同一敏感类别保留一次随机化记忆，防止反复上报平均掉噪声。写供Go采集宿主调用的双层随机化包，首次按给定有理矩阵生成永久状态并可靠保存，每次上报再按另一矩阵生成即时结果；永久状态绑定设备、字段和参数版本，重试不能重新抽取。用可中断存储测试并发首次上报、参数换代和响应丢失，独立枚举小矩阵核对联合输出分布。

## 设计

包 `netsec` 实现 RAPPOR 风格的双层随机化：

- 第一层永久随机化（PRR）：同一（设备, 字段, 参数版本）只按 `Permanent` 矩阵抽取一次，结果通过 `Store` 可靠持久化；重试、并发或进程重启都复用该永久状态，不会重新抽取。
- 第二层即时随机化（IRR）：每次 `Report` 按 `Instantaneous` 矩阵从永久状态重新抽取即时上报值，不落盘。
- 概率以精确有理数 `Rational{Numerator, Denominator}` 给出，行内按分母最小公倍数解析为整数权重抽样，无浮点误差；权重溢出返回错误。
- 存储键由设备、字段、参数版本经 SHA-256 派生；记录额外携带两层矩阵与版本的参数指纹，同版本号下矩阵被替换会返回 `ErrFingerprintMismatch` 而不是静默复用。

## 用法

```go
store, err := netsec.NewFileStore("/var/lib/telemetry/memo")
if err != nil { /* ... */ }

reporter, err := netsec.NewReporter(netsec.Config{
    DeviceID:      deviceID,
    Field:         "browsing_history",
    ParamVersion:  "v3",
    Permanent:     prrMatrix, // n×n，每行概率和为 1
    Instantaneous: irrMatrix, // n×n，每行概率和为 1
    Store:         store,
    // Source 留空则使用并发安全的 math/rand/v2 全局随机源
})
if err != nil { /* ... */ }

// 首次调用抽取并落盘永久状态；之后（含重试、重启）只复用记忆，
// 每次调用再独立做一次 IRR 抽取。
reportedSymbol, err := reporter.Report(context.Background(), truthSymbol)
```

## 测试

- `reporter_test.go`：并发首次上报（64 goroutine 仅一次 PRR 抽取/一次提交）、参数换代、指纹不匹配、提交失败重试、响应丢失不重抽、context 取消。
- `filestore_test.go`：临时文件 + fsync + 原子 rename 流程，在五个写入阶段注入中断，验证半成品对 `Load` 不可见且重试成功；另有损坏检测、键隔离与并发提交。
- `enum_test.go`（外部测试包）：独立实现有理权重解析，枚举 2×2（分母 4 与 3）和 3×3（分母 2、含零权重）小矩阵的全部抽样分支，逐格核对包输出的联合分布与手算有理分布一致，并验证两次上报因记忆化而相关。

运行：

```sh
go test -race -count=1 ./...
```
