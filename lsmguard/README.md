# lsmguard — LSM 读放大与 Bloom Filter 误判率监控器

对 LSM 存储引擎的性能度量诊断器（纯 Go 标准库实现）：

- **读放大监控**：统计每次 `Get` 实际读取的 SSTable 数量（滑动窗口均值）。
- **误判率监控**：统计 Bloom Filter 阳性结果中实际不存在的比例。
- **动态建议**：读放大超阈值 → 建议增加 Compaction 并发/权重；误判率超阈值 → 建议重建 Filter。

## 结构

- `bloom.go` — 布隆过滤器（双重哈希，内置误判统计）
- `engine.go` — 简化 LSM 引擎：MemTable + Flush 成 SSTable，`Get` 逐层探测并上报度量样本
- `monitor.go` — 滑动窗口聚合 + 阈值告警（带去抖冷却）
- `cmd/demo/main.go` — 验收场景演示

## 运行

```sh
export GOCACHE=/private/tmp/gocache   # 本机沙箱需要，正常环境可省略
go test -v ./...    # 测试
go run ./cmd/demo   # 演示
```

## 验收场景

写入随机散列 Key 形成 4 个 SSTable；故意关闭部分 Bloom Filter 后，
监控器即时捕捉到读放大从 ~1.05 跃升至 ~4.0，输出告警建议增加
Compaction 权重压制读延迟。
