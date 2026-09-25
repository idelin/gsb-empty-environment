# versioned-walks

Python 3.11（仅标准库）实现的**版本化有向多重图**上的长度区间游走计数与字典序第 k 条游走查询。

## 功能

- **版本化图**：顶点、边均为字符串 ID；保留孤立点与多重边；每次增删提交新版本，旧版本快照永久可查询。
- **区间计数** `count(s, t, lo, hi)`：长度区间左闭右开 `[lo, hi)`；长度 0 仅当 `s == t` 时计 1 条。
- **大整数与可选模数**：精确计数使用 Python 任意精度整数；`modulus=` 可选取模。模计数不得驱动第 k 选择（`kth(..., modulus=...)` 直接报错），两种结果不混用排序选择。
- **增量失效**：矩阵幂块 `P[i]=A^(2^i)` 与前缀和块 `S[i]=sum A^L` 按 `(version, modulus)` 缓存；边增删产生新版本键，旧块即失效，旧版本缓存保留。
- **可达切片**：计数结果附 `reachable_slice`（从 s 可达且能到达 t 的顶点集）。
- **无限模式** `hi=None`：若相关切片内存在环，返回 `infinite=True` 及循环证据（到达路径、环边、离开路径）；无关环（不可达 s 或到不了 t）不影响；无环时返回精确有限计数。
- **第 k 条查询** `kth(s, t, lo, hi, k)`：按边 ID 序列字典序（前缀优先）选取；`max_length` 限制输出轨迹长度防爆内存；`budget` 耗尽抛 `BudgetExhausted`，绝不误报无路径（`NoSuchWalk` 仅在确证 k 超出总数时抛出）。
- **反向排名** `rank(...)`：第 k 查询的逆运算，`rank(kth(k)) == k`。

## 运行

```bash
python3.11 example.py                          # 可运行样例
python3.11 -m unittest discover -s tests -v    # 测试（含小图枚举核验）
```

## 结构

- `versioned_walks/graph.py` — `VersionedGraph`：版本提交、快照、查询入口
- `versioned_walks/engine.py` — `CountEngine`：矩阵幂/前缀和 DP 块，精确或取模
- `versioned_walks/analysis.py` — 可达切片、环证据（BFS + 三色 DFS）
- `versioned_walks/kth.py` — 第 k 条选择与反向排名，预算与长度限制
- `versioned_walks/results.py` / `errors.py` — 结果类型与异常
- `tests/test_versioned_walks.py` — 25 个测试，含随机小图暴力枚举对拍
