# dictcol — 紧凑字典编码列上的向量化比较与投影器

直接在整数字典 ID 向量上执行批量操作的优化算子：

- **字典编码**：字符串列编码为 1 字节/行的整数 ID 数组（1024 行 = 1024 字节）。
- **谓词下推**：`status IN ('PAID','REFUNDED')` 在查询开始时一次性转换为整数 ID 集合，
  之后全程在整数域运算，不再触碰字符串。
- **向量化比较**：SWAR 字并行扫描，每个 64 位字单周期处理 8 行，
  使用无跨字节进位的精确判等公式 `(b&0x7f)+0x7f`，输出行位掩码（bitmask）。
- **延迟解码**：过滤、投影中间结果始终保持整数 ID（`ProjectCodes`），
  仅在最终输出时反查字典还原字符串（`Project`）。

## 文件

- `dict.go` — 字典（字符串 ⇄ 整数 ID）
- `column.go` — 字典编码列（`[]byte` 紧凑存储）
- `filter.go` — SWAR 向量化 `IN` 过滤算子，输出位掩码
- `project.go` — 投影器：输出时解码 / 保持整数域
- `cmd/demo/main.go` — 运行样例

## 运行样例

```
$ go run ./cmd/demo
rows=10 encoded=10 bytes (1 byte/row)
dictionary: PENDING->0 PAID->1 REFUNDED->2
encoded codes: [0 1 2 1 0 2 1 0 1 0]

filter: status IN (PAID, REFUNDED)
bitmask: 01101110 (matched 6/10 rows)
projected: [PAID REFUNDED PAID REFUNDED PAID PAID]
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestAcceptance_StatusInPaidRefunded_1024Rows   # 验收场景：1024 字节数组上的向量化掩码比对
--- PASS: TestAcceptance_StatusInPaidRefunded_1024Rows (0.00s)
=== RUN   TestDictionaryRoundTrip
--- PASS: TestDictionaryRoundTrip (0.00s)
=== RUN   TestFilterIn_TailRows                          # 非 8 倍数行的标量尾部处理
--- PASS: TestFilterIn_TailRows (0.00s)
=== RUN   TestFilterIn_NoMatchAndUnknownValue
--- PASS: TestFilterIn_NoMatchAndUnknownValue (0.00s)
=== RUN   TestProjectCodes_StaysInIntegerDomain
--- PASS: TestProjectCodes_StaysInIntegerDomain (0.00s)
PASS
ok  	dictcol

$ go test -run XXX -bench .
BenchmarkFilterIn_1024-10    100000    22621 ns/op   # 1024 行 IN 过滤（含掩码分配）
```
