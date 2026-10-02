# kmmatcher — 带权二分图最大权匹配与增广路径器

基于 **Kuhn–Munkres（KM / 匈牙利）算法** 的加权二分图最大权匹配器，纯 Go 标准库实现。

## 模型

- 左侧为工人（worker），右侧为任务（task），边权表示产值；
- 输出使**全局总产值最大**的匹配方案；
- 允许工人/任务未匹配：内部把图补全为 `(workers+tasks)²` 方阵，
  每侧配虚拟节点（权 0），不存在的边与 0 权边对最优总产值等价，
  因此每个真实顶点都可以“逃”到虚拟节点而保持未匹配。

## 功能

| 能力 | API |
| --- | --- |
| 全量求解（O(n³)） | `Match()` |
| 增量修复重算 | `Rematch()` |
| 动态调整单条边权 | `UpdateEdge(u, v, w)` |
| 屏蔽/恢复节点（工人请假下线） | `MaskWorker` / `UnmaskWorker` / `MaskTask` / `UnmaskTask` |
| 建边/删边（不触发重算） | `SetEdge` / `RemoveEdge` |
| 读取结果与未匹配最小机会成本 | `Result()` |
| 读取顶标（对偶变量） | `Labels()` |

**增量修复（Rematch）流程**：

1. 局部重置顶标：边权升高可能破坏对偶可行性 `lx[u]+ly[v] >= a[u][v]`，
   把受影响左点的 `lx` 提升到恰好可行，尽量保留原有紧绷边；
2. 断开不再紧绷（`lx+ly != a`）的匹配边；
3. 从每个失配左点出发，在相等子图中寻找**交错增广轨**，
   不存在时按最小 slack 调整顶标扩大相等子图，直至增广成功。

**机会成本**：未匹配顶点的机会成本 = 其到对侧未屏蔽顶点的最大边权
（让渡匹配机会所放弃的最大产值）；`Result.MinOpportunityCost`
为所有未匹配顶点机会成本的最小值。

## 运行样例

```sh
go run ./cmd/demo
```

输出（4×4 完全二分图，W0 为高产值工人）：

```
=== 初始最大权匹配（KM 全量求解） ===
  工人 W0 -> 任务 T0  (产值 100)
  工人 W1 -> 任务 T3  (产值 14)
  工人 W2 -> 任务 T2  (产值 13)
  工人 W3 -> 任务 T1  (产值 15)
  全局总产值: 142
  未匹配最小机会成本: 0

=== W0 请假下线（MaskWorker + 增量 Rematch） ===
  工人 W1 -> 任务 T3  (产值 14)
  工人 W2 -> 任务 T2  (产值 13)
  工人 W3 -> 任务 T1  (产值 15)
  全局总产值: 42
  未匹配任务: [0]
  已下线工人: [0]
  未匹配最小机会成本: 10

=== W1->T1 产值上调为 30（UpdateEdge + 增量 Rematch） ===
  工人 W1 -> 任务 T1  (产值 30)
  工人 W2 -> 任务 T2  (产值 13)
  工人 W3 -> 任务 T3  (产值 10)
  全局总产值: 53
  ...

=== W0 复工（UnmaskWorker + 增量 Rematch） ===
  工人 W0 -> 任务 T0  (产值 100)
  工人 W1 -> 任务 T1  (产值 30)
  工人 W2 -> 任务 T2  (产值 13)
  工人 W3 -> 任务 T3  (产值 10)
  全局总产值: 153
```

## 测试

```sh
go test ./...
```

测试结果（Go 1.24）：

```
=== RUN   TestBasicPerfectMatching          --- PASS   # 3×3 经典例子，验证最优总产值
=== RUN   TestRectangular                   --- PASS   # 非方阵（2×4 / 4×2）与未匹配输出
=== RUN   TestRandomVsBruteForce            --- PASS   # 300 组随机图与指数级暴力枚举对拍
=== RUN   TestDynamicUpdatesVsBruteForce    --- PASS   # 120 组 × 20 步随机动态操作逐步对拍
=== RUN   TestMaskHighValueWorker           --- PASS   # 验收场景：高产值工人下线→次优切换→复工恢复
=== RUN   TestUpdateEdgeSwitch              --- PASS   # 单边调权引起匹配切换与回退
=== RUN   TestOpportunityCost               --- PASS   # 未匹配最小机会成本取值
=== RUN   TestLabelsFeasible                --- PASS   # 动态操作后顶标对偶可行性 lx+ly>=a
=== RUN   TestEmptyAndSingle                --- PASS   # 空图、单边、负权边保持未匹配
PASS
ok      kmmatcher
```

其中 `TestRandomVsBruteForce` 与 `TestDynamicUpdatesVsBruteForce`
用指数级枚举的暴力解作为基准，验证全量求解与增量修复在随机图、
随机屏蔽/调权序列下始终输出全局最优总产值。

## 备注

- 边权建议范围 `|w| <= 1<<50`，保证顶标运算不溢出；
- 复杂度：全量 `Match` 为 O((n+m)³)，单次增量 `Rematch` 通常远小于全量
  （每个失配顶点一次 O((n+m)²) 的增广）。
