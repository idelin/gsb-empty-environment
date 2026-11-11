# DNS隧道特征窗口

网络探针需要把异常域名查询聚合为可解释特征，不能因时间乱序重复计数。给Go检测宿主写可导入的窗口组件，按客户端和调用方给定的注册域分组，精确统计标签长度分布、不同标签数、NXDOMAIN比例和字节熵；固定半开窗口，水位线封口，重复事件ID不重复加入。可修正封口前事件，修正后须撤回旧贡献。用全量重算对照乱序、修正和ID冲突。

## 包

可导入包 `example.org/netsec/uid1260/dnswindow`，无第三方依赖，仅用标准库，
所有公开类型带文档注释，`Manager` 可被多 goroutine 并发使用。

## 语义

- **固定半开窗口**：窗口大小在 `NewManager` 固定，按 Unix epoch（UTC）对齐，
  每个窗口覆盖 `[Start, End)`。
- **水位线封口**：`Advance(t)` 把水位线推进到 `t`，所有 `End <= t` 的窗口被
  封口并以不可变 `WindowResult` 按时间顺序返回；`t <= 水位线` 的调用为空操作。
- **乱序安全**：只要事件时间晚于水位线，乱序到达也进入对应窗口精确聚合；
  时间 `<= 水位线` 的事件返回 `ErrLate`，不会计入已封口窗口。
- **ID 去重**：事件 ID 在 `Manager` 全生命周期内只计一次。同 ID 完全相同的
  重复提交返回 `OutcomeDuplicate`；已封口 ID 被墓碑化，任何再提交返回
  `ErrSealed`，杜绝回放重复计数。
- **封口前修正**：同 ID 的新版本返回 `OutcomeCorrected`，先从旧版本所在
  窗口/分组撤回全部贡献（计数器、标签直方图、不同标签数、字节频率），再把
  新版本计入；修正可跨分组、跨窗口移动。新版本时间迟到则整体拒绝
  （`ErrLate`），旧贡献保持不变。

## 特征

每个 `(客户端, 注册域)` 分组在封口时产出 `GroupFeatures`：

- `LabelLengthHist`：标签长度 → 标签出现次数的精确分布；
- `DistinctLabels`：去重后的不同标签字符串数；
- `NXDOMAINRatio`：`NXDOMAINEvents / TotalEvents`；
- `ByteEntropy`：查询名字节的 Shannon 熵（以 2 为底，单位 bit/byte），
  同时暴露 `TotalNameBytes` 与 `ByteFrequency [256]int64` 供宿主复核；
  名字末尾单个 `.` 在标签切分与字节统计中统一忽略。

## 用法

```go
manager, _ := dnswindow.NewManager(time.Minute)

outcome, err := manager.Submit(dnswindow.Event{
    ID: "evt-1", Client: "10.0.0.1", RegisteredDomain: "evil.example",
    QueriedName: "aaaa.evil.example", NXDOMAIN: true, Time: ts,
})
// outcome: OutcomeAccepted / OutcomeDuplicate / OutcomeCorrected

for _, result := range manager.Advance(watermark) {
    for _, features := range result.Groups {
        // 读取 LabelLengthHist、DistinctLabels、NXDOMAINRatio、ByteEntropy
        _ = features
    }
}
```

## 测试

- `window_test.go`：手工精确校验聚合值、窗口对齐、乱序准入、修正撤回、
  跨窗口移动、封口拒绝、非法输入与并发（`-race`）。
- `differential_test.go` + `oracle_test.go`：随机工作流（乱序插入、精确
  重复、跨组/跨窗口修正、迟到拒绝、封口 ID 冲突）下，用独立的全量重算
  预言机（自然对数实现熵、每次封口从零重建所有计数）对照增量 `Manager`，
  每个封口点逐字段比较。

```sh
go test ./... -race -count=1
go test ./dnswindow/ -cover
```
