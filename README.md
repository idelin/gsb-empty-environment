# 块编码选择器 (Block Encoding Selector)

从每个图块的候选编码方案中各选一项：在总字节不超预算、且每个所选方案
最大误差不超过限制的前提下，使整图总平方误差 (SSE) 最小。输出精确最优解、
总字节与预算余量。

## 构建与运行

```sh
c++ -std=c++17 -O2 -o selector main.cpp
./selector < input.txt
```

## 输入格式 (stdin)

```
n budget max_err_limit
# 以下重复 n 次（每个图块）：
k
bytes max_err sse      # 共 k 行，每行一个候选方案
```

## 输出格式 (stdout)

```
total_sse
total_bytes remainder   # remainder = budget - total_bytes（预算余量）
c0 c1 ... c(n-1)        # 每块所选候选的原始下标（0 起）
```

无可行解时输出 `INFEASIBLE`。

## 算法

多选背包精确 DP：`dp[b]` = 已处理块恰好用 b 字节时的最小总 SSE，
逐块转移，最后取 `b <= budget` 的最优点。复杂度 `O(总候选数 × budget)`，
回溯表空间 `O(n × budget)`。

## 支配裁剪

每块内先丢弃 `max_err > max_err_limit` 的不可行方案；再裁剪被支配方案：
若 A.bytes ≤ B.bytes 且 A.sse ≤ B.sse，则 B 被 A 支配。`(bytes, sse)`
完全相同的并列方案只保留原始下标最小者，以不破坏决胜规则 3。

## 并列决胜规则（按序应用，确定性）

1. 总 SSE 更小者胜；
2. SSE 相同则总字节更少者胜（预算余量更大）；
3. 仍并列则保留现任解；块内候选按原始下标升序参与转移，
   因此达到最优的候选中原始下标最小者优先。
