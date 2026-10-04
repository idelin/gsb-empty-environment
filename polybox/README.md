# polybox — 整数多面体查询工具

在有限整数盒 `box = [lo_0,hi_0] x ... x [lo_{n-1},hi_{n-1}]` 内处理线性不等式，
回答两类查询（计数使用大整数 `math/big`）：

- `count`：盒内满足全部约束的整点个数；
- `kth`：按字典序（x0 为最高位）排名第 K 的整点（K 从 1 开始），
  并输出排名证明——逐维的前缀跳过计数，满足 `sum(skipped) + 1 == K`。

## 构建与运行

```sh
go build -o polybox .
cat input.json | ./polybox            # 可加 -budget N -verify -verify-limit N
```

## 输入（stdin, JSON）

```json
{
  "dims": 2,
  "box": [[0, 10], [0, 10]],
  "constraints": [{"a": [1, 1], "op": "<", "b": 5}],
  "queries": [{"type": "count"}, {"type": "kth", "k": "8"}],
  "budget": 1000000,
  "verify": true
}
```

- `op` 支持 `<=`、`<`、`>=`、`>`、`==`（严格不等式按整数语义化为 `<= b-1`，
  等式拆成两个方向）；
- 大整数（如 `b`、`k`）可写成 JSON 字符串；
- `budget` 为搜索节点预算，`0` 表示不限；输入中的 `budget` 覆盖命令行；
- `verify: true` 时对小盒（点数不超过 `verify_limit`）做全枚举核对。

## 输出（stdout, JSON）

- `count`：`{"status":"ok","count":"15"}`；
- `kth`：`{"status":"ok","point":["1","2"],"proof":[{"dim":0,"value":"1","skipped":"5","remaining":"3"}, ...]}`；
- 预算耗尽：`{"status":"unknown","detail":"budget exhausted"}`（不丢点、不猜数）；
- K 越界：`{"status":"out_of_range"}`；
- `verify` 段报告全枚举核对结果（`ok` / `mismatch` / `skipped`）。

## 算法与正确性

按维递归（x0 最高位）。每个节点对每个约束计算已固定部分 `cur` 与剩余维的
最小/最大可能后缀和（由盒边界预计算）：

- `cur + suffixMin > b`：可证明不可行，剪枝；
- 全部约束 `cur + suffixMax <= b`：剩余盒内所有点均可行，直接乘出点数
  （大盒无约束时 O(1) 完成）；
- 否则用约束对当前维做区间收紧（floor/ceil 除法，负系数翻转方向），
  只排除可证明无可行延拓的取值——界不够紧时继续分支，绝不丢点。

`kth` 复用同一计数做逐维前缀计数下钻，proof 即下钻过程记录的前缀计数。

## 测试

```sh
go test ./...
```

覆盖：严格边界、空维（`lo > hi` 与 0 维）、冗余约束、K 越界、预算耗尽、
等式约束、大整数盒，以及 300 组随机小盒与全枚举的计数/排名交叉核对。
