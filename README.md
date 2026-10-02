# fas — 有向图反馈弧集（FAS）极小近似器

破环切边优化器：求一个权重尽量小的边集，移除后使有向图成为 DAG，
输出切除边集与剩余图的拓扑序列，并验证严格无环。

## 算法

- **贪心破环**（`GreedyCycleCut`）：反复用 DFS 找一条有向环，切除环上
  权重最大的关键边（被环共享的高权重边会优先被定位切除），直到无环。
- **拓扑权重启发式**（`EadesOrder` + `FASFromOrder`）：加权
  Eades-Lin-Smyth 排序（汇点置尾、源点置首，否则取出权−入权最大者），
  所有逆序边构成 FAS。
- **混合**（`Hybrid`，推荐入口）：取两种启发式中切集权重更小者，再做
  极小化（`Minimize`）——凡能放回而不成环的边一律放回，保证切集按
  包含关系极小。

## 证明与验证

每个结果都满足（测试中逐条断言）：

1. `Removed + Remaining` 恰好重构原图边集；
2. `Remaining` 严格无环（Kahn 算法判定）；
3. 输出的拓扑序列对每条剩余边都满足 前驱 < 后继；
4. 切集极小：放回任意一条被切边都会重新成环。

## 运行

```sh
export GO111MODULE=on
go test ./... -v     # 测试
go run ./cmd/fasdemo # 样例：3 个交叠有向环的系统
```

样例输出（节选）：

```
algorithm: greedy-cycle-cut
  cut set (weight 6):
    cut B->C (w=5)        <- 共享权重最高的关键边
    cut D->E (w=1)
  topological order of remaining graph: C E A F D B

algorithm: hybrid(greedy+topological-weight)
  cut set (weight 4):
    cut A->B (w=2)
    cut D->B (w=1)
    cut F->D (w=1)
  topological order of remaining graph: B C A D E F
```
