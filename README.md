# 价格修复求解器（pricerepair）

给定每件商品的原始标价（部分商品**锁定**不可改），以及若干成对价差上下界
`min <= price[to] - price[from] <= max`，求满足全部约束的**整数**价格，使得：

1. **修改商品数最少**（第一目标，锁定商品永远不计入）；
2. 在修改数最少的前提下，**总绝对改价额最小**（第二目标）。

商品数最多 18 件。若约束系统不可行，输出**负环**作为不可行证据。

> 注意：两个目标是严格词典序的。求解器**不会**先最小化总改价额、再声称
> 修改数最少——例如 `examples/lexicographic.json` 中，全局最小总改价方案
> 要改 3 件（总改价 9），但正确答案是改 2 件（总改价 12）。

## 构建与运行

```sh
go build -o pricerepair .
./pricerepair -input examples/lexicographic.json
cat examples/simple.json | ./pricerepair
```

参数：`-input <文件>`（缺省读标准输入）、`-compact`（输出紧凑 JSON）。

## 输入格式（JSON）

```json
{
  "items": [
    {"id": "A", "price": 100, "locked": true},
    {"id": "B", "price": 120}
  ],
  "bounds": [
    {"from": "A", "to": "B", "min": 10, "max": 30}
  ]
}
```

- `items[].price`：原始标价（整数）；`items[].locked`：锁定则价格不可改。
- `bounds[]`：`min <= price[to] - price[from] <= max`，`min`/`max` 可各自缺省表示无界。

## 输出格式（JSON）

可行时：

```json
{
  "feasible": true,
  "prices": [
    {"id": "A", "price": 100, "originalPrice": 100, "locked": true, "changed": false}
  ],
  "modifiedCount": 1,
  "modifiedItems": ["B"],
  "totalAbsChange": 5
}
```

不可行时（`evidence.constraints` 按环序给出矛盾约束，权值和为负）：

```json
{
  "feasible": false,
  "evidence": {
    "type": "negative-cycle",
    "constraints": ["price[B] - price[A] <= 2", "price[B] >= 3（锁定）", "price[A] <= 0（锁定）"],
    "cycleSum": -1,
    "message": "负环不可行证据：沿环把以上 3 条约束相加，左端相互抵消为 0，右端之和为 -1，得到 0 <= -1，矛盾"
  }
}
```

输入非法（id 重复、商品数超过 18 等）时返回 `{"feasible": false, "error": "..."}`。

## 算法

把每条价差界限拆成差分约束边 `price[v] - price[u] <= w`：

1. **不可行判定**：价差边 + 锁定边（经接地节点表示 `p_i = 原价`）上跑
   Bellman-Ford，存在负环即不可行，并回溯父指针取出负环作为证据。
2. **最少修改数**：Floyd-Warshall 求全源最短路 `dist[a][b]`（即
   `price[b]-price[a]` 的最紧导出上界）。一组商品能同时保持原价，当且仅当
   两两满足 `orig[b]-orig[a] <= dist[a][b]`——这是相容图上的团。于是
   **最少修改数 = 未锁定商品数 − 相容图最大团大小**（n ≤ 18，
   Bron-Kerbosch 枚举全部最大团，上界 3^(n/3) = 729 个）。
3. **最小总改价**：对每个最大团（保持原价的集合），解
   `min Σ|p_i - orig_i|` s.t. 差分约束。该 LP 的对偶是**最小费用循环流**
   （全单模矩阵 ⇒ 整数最优），用连续最短路增广求解，再由残余图最优势
   还原整数价格。取所有最大团中的最优值（用区间下界剪枝加速）。

## 测试

```sh
go test ./...
```

包含手工用例（词典序、多最大团、负环证据、18 件链式）以及
小规模随机实例与指数级暴力枚举的对拍。
