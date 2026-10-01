# swhist — 滑动窗口直方图与高百分位数估算器

流式维护 P90/P99 延迟指标的 Go 组件，仅依赖标准库。

## 设计

- **混合分桶**：低延迟区间使用固定桶宽线性桶（默认 0–200ms，宽 0.05ms），
  长尾区间使用对数（几何）桶（默认比率 1.002，直至 600s），全范围相对误差有界。
- **滑动窗口**：容量 N 的环形缓冲区只保存每个样本的**桶索引**（uint32），不常驻原始样本。
  窗口满时，`Add` 弹出最旧槽位并对其桶计数做反向补偿（递减），
  使聚合桶计数始终精确等于最近 N 个样本，单次操作 O(1)。
- **分位数估算**：按桶计数做最近秩定位，再在目标桶内线性插值。
- **内存**：`O(桶数 + 窗口容量)`，默认配置约 8000 桶 ≈ 68KB。

### 误差理论界限

对落在桶 `[l, u)` 内的值，估算值与真值之差不超过桶宽 `u - l`，相对误差上界为：

- 线性区：`LinearWidth / v`
- 对数区：`LogRatio - 1`（默认 0.2%）

桶内插值使平滑分布上的实测误差远低于该上界（实测 P99 最差 0.023%）。

## 使用

```go
h, _ := swhist.New(swhist.DefaultConfig()) // 窗口容量 1000
h.Add(12.3)                                // 推入延迟样本（ms）
fmt.Println(h.P90(), h.P99())              // 流式读取分位数
```

自定义配置：`swhist.Config{Capacity, LinearMax, LinearWidth, LogRatio, MaxValue}`。

## 运行

```sh
export GO111MODULE=on          # Go 1.11 需要显式开启 module
go test -v ./...               # 运行验收测试
go run ./cmd/demo              # 运行流式样例
```

## 样例输出（go run ./cmd/demo）

```
window=1000 buckets=8008 (linear 0-200ms @0.05ms, log x1.002 up to 600s)

warmup: 1000 long-tail samples est  P50=   20.06ms P90=   54.87ms P99=  124.52ms
                             exact P50=   20.07ms P90=   54.88ms P99=  124.50ms  (P99 err 0.020%)

steady state after 5000 more est  P50=   20.71ms P90=   59.29ms P99=  154.13ms
                             exact P50=   20.70ms P90=   59.28ms P99=  154.13ms  (P99 err 0.001%)

spike: 20x 30000ms injected  est  P50=   21.03ms P90=   66.32ms P99=29979.22ms
                             exact P50=   21.01ms P90=   66.34ms P99=30000.00ms  (P99 err 0.069%)

spikes half expired (+500)   est  P50=   20.68ms P90=   62.62ms P99=29979.22ms
                             exact P50=   20.68ms P90=   62.61ms P99=30000.00ms  (P99 err 0.069%)

spikes fully expired (+1000) est  P50=   19.42ms P90=   56.82ms P99=  125.22ms
                             exact P50=   19.43ms P90=   56.82ms P99=  125.22ms  (P99 err 0.008%)
```

## 测试结果（go test -v ./...）

```
=== RUN   TestPercentileAccuracy
--- PASS: TestPercentileAccuracy (20.60s)
    worst relative error over 9001 steps: P90=0.0500% P99=0.0233%
=== RUN   TestSpikeEviction
--- PASS: TestSpikeEviction (0.00s)
    baseline P99: est=163.175ms exact=163.176ms
    spiked  P99: est=29979.215ms exact=30000.000ms
    recovered P99: est=113.025ms exact=113.036ms
=== RUN   TestReverseCompensation
--- PASS: TestReverseCompensation (0.22s)
=== RUN   TestEdgeCases
--- PASS: TestEdgeCases (0.00s)
=== RUN   TestInvalidConfig
--- PASS: TestInvalidConfig (0.00s)
PASS
ok      swhist  21.872s
```

### 验收边界覆盖

- 窗口容量 1000，连续压入长尾（对数正态）延迟数据；
- `TestPercentileAccuracy`：10000 步流式压入，每步与全量保留样本的精确
  P90/P99 对比，最差相对误差 P99 = 0.023%（≤ 0.5%）；
- `TestSpikeEviction`：注入 20 个 30s 极端毛刺，P99 立即响应（163ms → 30s），
  毛刺滑出窗口后灵敏回落（113.025ms vs 精确值 113.036ms，误差 0.01%）；
- `TestReverseCompensation`：10 万样本冲刷 1000 容量窗口，逐桶校验反向补偿
  后的计数与全量保留参考完全一致。
