# polyquery — 整数多面体查询工具

在有限整数盒 `lo[i] <= x[i] <= hi[i]` 内处理线性不等式组
`sum(coeffs[i] * x[i]) <= rhs`，支持：

- **count**：合法点计数（大整数，字符串输出）。
- **kth**：字典序第 K 个点（K 为 1 起始的大整数），并输出排名证明。
- **enumerate**：小盒全枚举（默认上限 10000 点，超出返回 `too_many`）。

## 正确性保证

- 剪枝只用盒边界的区间算术：当约束尾部和的**最小值**已超出 rhs 时剪枝
  （子树无可行点），当**最大值**已满足 rhs 时丢弃该约束（子树内冗余）。
  界不够紧时继续分支，绝不丢点。
- 无活跃约束的子树按盒体积闭合计算（各维宽度相乘），不逐点枚举。
- 排名证明：每一维记录 `skipped`——同前缀、该维取值更小的合法点数，
  各维 `skipped` 之和恒等于 `K-1`。
- 预算（节点访问数）耗尽时返回 `unknown`，不返回部分结果。

## 输入（stdin，JSON）

```json
{
  "dim": 2,
  "box": [[0, 4], [0, 4]],
  "constraints": [{"coeffs": [1, 1], "rhs": 5}],
  "budget": 1000000,
  "queries": [
    {"type": "count"},
    {"type": "kth", "k": 10},
    {"type": "enumerate", "limit": 100}
  ]
}
```

- 整数可写成 JSON 数字或字符串；计数、K、坐标一律按大整数处理。
- `budget`：0 或缺省为 1,000,000；负数表示不限。
- `dim: 0` 合法：空维空间只有空元组一个候选点，约束退化为 `0 <= rhs`。

## 输出（stdout，JSON）

每个查询一个结果，`status` 取值：

- `ok`：`count` 给出 `count`；`kth` 给出 `point`、`proof`（每维
  `{dim, value, skipped}`）和 `total`。
- `unknown`：预算耗尽。
- `out_of_range`：K 越界（附 `total`）。
- `too_many`：枚举超出 `limit`（附已截断的点）。
- `error`：查询本身非法（如缺 `k`）。

## 构建与测试

```sh
go build -o polyquery .
go test ./...
./polyquery < input.json
```

测试用固定种子随机生成小盒（含空维、零系数、冗余约束、反转区间），
与全枚举逐点核对计数、每个排名的第 K 点及证明中的前缀计数；另有严格
边界、K 越界、预算耗尽和 10^30 级大整数计数的专项用例。
