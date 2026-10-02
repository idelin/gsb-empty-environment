# streamjoin

带否定事件的流式时序 Join 组件（Go，仅标准库）。

## 语义

- 左流事件 `L` 到达后等待 `window`（默认样例为 5s）内的右流匹配事件 `R`。
- 窗口内 `R` 到达 → 立即输出 `PAIRED`。
- 超时无匹配 → 输出 `LEFT-ONLY`（孤立结果）。
- 超时前到达反向取消事件 `CANCEL` → 放弃等待，不输出任何结果。
- 迟到补配：`R` 的事件时间落在窗口内但处理时间迟到（左流已输出
  `LEFT-ONLY`）时，在 `lateness` 容忍期内输出 `PAIRED(late=true)` 补偿结果。
- 乱序支持：`R` 先于 `L` 到达时被缓冲，`L` 到达后立即配对；缓冲与补偿
  记录都有保留期，到期自动清理，状态零泄漏（`Stats()` 全部归零）。

## 结构

- `joiner.go` — Joiner 核心：待匹配左流、乱序右流缓冲、迟到补偿记录。
- `clock.go` — `Clock` 抽象：`RealClock`（生产）与 `ManualClock`（确定性测试/演示）。
- `joiner_test.go` — 验收测试（`go test ./...`）。
- `cmd/joindemo/main.go` — 运行样例（`go run ./cmd/joindemo`）。

## 运行

```sh
go test ./... -v
go run ./cmd/joindemo
```

## 并发说明

`Joiner` 内部加锁，可并发调用 `Handle`；`emit` 回调在锁外触发，
回调内不应再调用 Joiner 方法。
