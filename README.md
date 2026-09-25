# 多分辨率 min/max/能量索引（Python 3.11，标准库 + unittest）

从空索引构建的**持久化（copy-on-write）二叉金字塔**，为音频类样本序列维护
逐层级的最小值、最大值、能量（平方和）以及覆盖/缺测计数。样本数组与采样率
全部来自本地确定性夹具（`multires_index/fixtures.py`），**不接任何音频设备、
文件或网络**。

## 构建与运行

```bash
python3.11 demo.py                # 运行样例
python3.11 -m unittest test_multires -v   # 测试套件（21 个用例）
```

## 数据结构

- 时间线是一棵**不可变二叉金字塔**：第 `level` 层的一个节点恰好覆盖
  `2**level` 个样本；内部存储为若干**完整的二幂树块**（等价于总长度二进制
  中每个置位的 1，块层级严格递减）。
- 每个节点保存 `min`、`max`、`energy=Σx²`、`present`、`missing`。
  - `min/max` 独立于任何均值维护：**单样本尖峰在每一层都可见**，不会被
    平均摊平掩盖。
  - 能量按**真实覆盖样本**求和：缺测（`NaN`）样本既不进能量也不进计数，
    均值能量 = `energy / present`（绝不除以名义桶容量）。

## 范围查询：边缘精算

`query(lo, hi)` 把 `[lo, hi)` 递归分解为**完全落在区间内部**的对齐二幂节点：

- 边界处自动下降到更小的对齐节点（直到单个样本），因此**非对齐区间绝不会
  包含范围外的峰**；
- 摘要返回 `samples / present / missing / min / max / energy /
  mean_energy / full_buckets / partial_buckets / boundary_levels /
  buckets / visits / complete`；
- **部分桶绝不计作完整桶**：触边界的小桶、或缺测桶（`present < 容量`）
  统一记为 partial，逐桶信息在 `buckets` 中给出；
- 空区间返回 `min=max=None, energy=0`，峰值查询返回 `peak=None`、无伪峰。

## 峰值与并列溯源

`peak(lo, hi)` 返回：

- `peak`：区间最大值；
- `ranges`：**所有**取到峰值的“最大对齐来源范围”（粗桶若只是最大值等于峰
  但内部含非峰样本，会继续下降，不会被当成来源）；
- `sample_ranges` / `sample_indices`：精确到样本的全部并列位置与连续段。

## 版本化区间替换与增量修复

- `append` / `replace_range(lo, hi, values)` / `insert` / `delete` /
  `withdraw`（标记缺测、不移动后续样本）/ `restore` 都发布**新版本**，
  旧版本保持可寻址，`rollback(v)` 再发布一个指向旧状态的新版本头。
- 编辑通过持久化分裂/拼接实现：分裂路径外的子树直接共享（`reused`），只
  复制路径节点与二幂合并进位节点（`copied`），并统计访问的旧节点数
  （`visits`）。这些计数在 `Version` 上返回，用于验证**局部维护**。

## 有理采样率

采样率是精确的 `fractions.Fraction`。`change_rate(p/q)` 只发布元数据版本
（`copied=0`，不移动样本）；`time_at_index` / `index_at_time` /
`query_time` 的时间—样本映射全程分数精确，无浮点漂移。本实现做的是**速率
重标定（relabel）**，不做重采样插值（无新增/删除样本）。

## 延迟 / 复杂度与数值容差（声明）

| 操作 | 复杂度（n=长度） |
| --- | --- |
| 构建 / 追加 | 总计 O(n) 节点，追加摊销 O(k) |
| 区间替换 / 插入 / 删除 / 撤回 | O(k + log n) 复制，O(log n) 路径访问与进位，其余子树共享 |
| 范围查询 / 峰值 | O(log n) 节点访问（每边一条对数路径），分解块数 O(log n) |
| 速率变更 | O(1) |

递归深度上界 `ceil(log2 n)`。

数值容差：峰值/最小/最大按精确相等判定；能量逐节点为 `x*x` 的双精度和，
查询用 `math.fsum` 汇总，测试对逐样本参考采用相对容差 `1e-12`、绝对容差
`1e-9`。

## 验证方式

`test_multires.py` 用固定种子随机执行 300 步**剪切（replace）/插入/删除/
撤回**序列，每步同步维护一个普通 Python 列表作为逐样本参考，反复对随机
区间比较 min/max/能量/缺测计数/全部并列峰；另含尖峰逐级可见、非对齐边缘、
部分桶记账、空区间、持久化/回滚、有理速率映射、局部维护计数上限等用例。

## 文件

- `multires_index/multires.py`：核心实现（持久化金字塔、查询、版本、速率）
- `multires_index/fixtures.py`：本地确定性样本夹具
- `test_multires.py`：unittest 测试套件
- `demo.py`：运行样例
