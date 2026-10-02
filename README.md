# Maglev 一致性哈希查找表（Go 标准库实现）

实现 Google Maglev 论文的无锁恒定查找表（Lookup Table）生成算法：

- 每个节点由两个独立哈希（FNV-1a、CRC64-ECMA）生成全局排列偏好序列
  `permutation[j] = (offset + j*skip) mod M`；
- 查找表固定为素数大小（默认 65537），轮询填充直至填满；
- 查表为一次哈希 + 一次数组下标，O(1)；表构建后不可变，读路径无锁；
- 不 materialize N×M 排列矩阵，按 `next[]` 游标惰性求值，内存仅 O(M + N)。

## 目录

- `maglev/maglev.go` — 算法实现（`Build` / `Lookup` / `Distribution` / `ChangedSlots`）
- `maglev/maglev_test.go` — 均匀性、最小扰动、键漂移、基准测试
- `cmd/demo/main.go` — 运行样例：建表 → 杀第 2 个节点 → 重建 → 漂移与延迟统计

## 运行

```sh
go test ./maglev/ -v          # 单元测试
go test ./maglev/ -run '^$' -bench .   # 基准
go run ./cmd/demo             # 演示
```

## 实测结果（5 节点，M = 65537）

- 槽位分布：`[13108 13108 13107 13107 13107]`，与理想值 13107.4 最大偏差 1 槽（0.0006%）
- 杀死第 2 个节点重建：20.1001% 槽位易主（理论最小值 20.0000%）
- 20 万键模拟：幸存节点上的键漂移率 0.14%（理论应为 ~0）
- 查找延迟：约 235 ns/op（常数级，主要为 key 哈希开销）；建表约 1.3 ms
