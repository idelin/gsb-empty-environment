# segsolver

连续分段最低费用求解器（Go）。

## 模型

- 序列长 `n`，切点落在内部位置 `1..n-1`，每段长度 ∈ `[minLen, maxLen]`
- 段成本由整数表 `cost[i][j]`（段 `[i,j)`）给出，**允许负成本**
- 支持必须切（`must`）与禁止切（`forbid`）位置；同一位置既必须又禁止即冲突，构造时报错并列出冲突位置
- 目标：总费用最低；同价时取切点列表**字典序最小**（严格前缀更小，如 `[2] < [2,5]`）

## 用法

```go
s, err := segsolver.NewSolver(n, minLen, maxLen, cost, must, forbid)
res := s.Solve()                  // res.Cost 最优值, res.Cuts 切点证据
res  = s.SolveWithBudget(budget)  // 预算不足时仍返回最便宜合法切法作为合法上界
err  = s.Revise(i, j, newCost)    // 局部成本修订，下一次 Solve 生效
c, err := s.Evaluate(res.Cuts)    // 证据校验：重算切点列表的费用
err  = s.VerifyBruteForce()       // n<=20 时枚举全部 2^(n-1) 切分交叉复算
```

## 实现要点

- **反向 DP + 持久链表**：字典序 tie-break 不能用正向 DP「每位置只留最优列表」——当一个列表是另一个的前缀时，追加公共后缀会反转大小关系；前插公共前缀则保序，故从右往左递推，`[j] ++ best[j]` 直接比较完整候选列表
- 必须切通过「段不得跨越 must 位置」强制；禁止切通过「该位置不得成为段边界」强制
- `VerifyBruteForce` 对冲突构造、负段成本、不可行情形逐一与暴力枚举对拍（随机测试 3000 轮覆盖）

## 测试

```bash
GOCACHE=/tmp/gocache go test ./... -v
```
