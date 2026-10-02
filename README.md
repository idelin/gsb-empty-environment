# dictvec — 字典编码列上的向量化比较与投影器

纯 Go 标准库实现。字符串列一次性编码为紧凑整数 ID 向量（1 字节/行），
所有过滤/投影算子直接在整数数组上批量执行；字符串字面量通过字典转换下推
（dictionary transform pushdown）在编谓词阶段转成整数，直到最终结果输出时
才反查字典还原真实字符串。

## 结构

- `dictenc/dictionary.go` — 双向字典（string ↔ uint32 ID）
- `dictenc/column.go` — 字典编码列（共享字典 + `[]byte` ID 向量）
- `dictenc/compare.go` — 向量化比较内核：
  - `Compare(EQ/NE/LT/LE/GT/GE, literal)`：字面量下推为 ID 后做整数比较；
    EQ/NE 用 SWAR 内核，每 8 行一组在单个 64 位字内完成匹配
  - `In(literals...)`：字面量集合下推为 256 项整数查找表，扫描即单次索引
- `dictenc/project.go` — `Project(mask)` 按掩码投影（输出仍是字典 ID，
  共享字典），`Materialize()` 仅在输出时反查字典
- `cmd/demo` — 可运行样例

## 运行

```sh
go test ./... -v        # 测试（含验收场景）
go test ./dictenc -bench .   # 基准
go run ./cmd/demo       # 样例
```

## 验收场景

状态列 `['PENDING','PAID','REFUNDED']`，过滤 `status IN ('PAID','REFUNDED')`：
1024 行编码为 1024 字节整数数组，`In` 将两个字面量下推为 ID 查找表后
向量化扫描，一次遍历完成全部行匹配（`TestAcceptanceStatusInFilter`）。

## 实测结果（go1.11.4 darwin/amd64）

```
=== RUN   TestAcceptanceStatusInFilter
--- PASS: TestAcceptanceStatusInFilter (0.00s)
... 全部 6 个测试 PASS ...
BenchmarkStatusInFilter1024-10   1000000    6504 ns/op   157.42 MB/s
BenchmarkStatusEQ1M-10                100  12933010 ns/op   81.08 MB/s
```

demo 输出：

```
column: 1024 rows, dictionary-encoded to 1024 bytes (1 byte/row, 3 distinct values)
filter: status IN ('PAID','REFUNDED') -> integer mask, 682/1024 rows matched
project: 682 rows kept, still dictionary IDs (no string touched yet)
output: first 8 decoded values: [PAID REFUNDED PAID REFUNDED PAID REFUNDED PAID REFUNDED]
```
