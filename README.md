# bitmapsched — 压缩位图 + 多租户隔离与配额公平调度器 (Go)

纯标准库实现，无第三方依赖。

## 模块

- `bitmap/` — Roaring 风格压缩位图（uint64 键空间，高 48 位分桶）
  - 三种容器自动转换：`ArrayContainer`（≤4096 离散值，2B/值）、
    `BitmapContainer`（8KiB 位图）、`RunContainer`（RLE 区间，4B/区间）；
    每次写入/集合运算后按"存储字节最小"自动选择最优编码
  - 批量集合运算：`And` / `Or` / `Xor`（可变参数），
    `AndCardinality` 不物化结果直接计数（字级 popcount / 区间扫描）
  - `Add`（离散点）、`AddRange`（连续区间）、迭代器、克隆、统计
- `scheduler/` — 多租户配额隔离 + 加权 Deficit Round Robin 公平调度
  - 每租户配额：`Weight`（DRR 权重）、`MaxQueued`（排队上限）、
    `MaxInflight`（并发执行上限）
  - 租户内 FIFO；空队列赤字清零；赤字上限防止突发不公平；
    动态上限保证大 cost 任务不饿死
  - `Store`：按租户命名空间隔离的位图存储，所有写入经调度器配额准入
- `cmd/demo/` — 运行样例

## 运行

```sh
go test ./... -race        # 全部测试（含竞态检测）
go test ./bitmap/ -bench=. # 位图运算基准
go run ./cmd/demo          # 样例：容器选择、百亿级交并、DRR 公平性
```
