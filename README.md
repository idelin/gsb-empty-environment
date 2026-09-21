# 条件依赖暴露分析器（condexpose）

Python 3.11 标准库实现，读取本地 JSON，无第三方依赖、不联网。

## 要解决的问题

一个包的同一个符号可能同时从两条**公开路径**暴露，其中一条只在某个特性
（feature）开启时存在。分析器需要：

- 在**不逐一枚举特性配置**的前提下，给出每个暴露符号的**可达条件**、
  一个满足条件的配置以及对应的**供应路径**；
- 识别“不同来源在同一配置下供应同名符号”的冲突，给出共同可行配置和两条
  独立供应路径；条件互斥时**不得误报**；
- 同一来源经多条路径到达不冲突；来源身份 = `(包ID, 版本, 原符号名)`；
- 删除/修改边后**增量更新**，依赖环中的旧可达性不得因引用计数而永久残留；
- 区分 public / private / build 三种边。

## 边语义

| 边 | 语义 |
|---|---|
| `public` | root 沿 public 边得到运行闭包可达性；符号沿 public 边**向上传递**，每个导入者应用自己的 `exports`（重命名/隐藏） |
| `private` | root 只看到直接 private 依赖**自身**的导出；其内部依赖不继续向外暴露（私有传递阻断） |
| `build` | 只进入独立的**构建闭包**；build 符号不会出现在运行暴露中 |

根包自身可见“直接 public 依赖”与“直接 private 依赖”的导出；传递只沿
public。`exports` 是 `暴露名 -> 原符号名` 的映射，值为 `null` 表示隐藏；
同一原名可映射到多个暴露名（别名扇出）。

## 表示与算法

- `bdd.py`：**规范化有序布尔决策图（ROBDD）**。特性按固定顺序排列，
  `make` 唯一化 + 同支归约，配合 ITE 的香农展开保证规范形式；
  菱形/多源条件共享子图，节点数随特性数线性增长，与配置数 `2^N` 无关。
- `analyzer.py`：**带条件的最小不动点**。事实的值不是布尔而是 BDD 条件：
  - 支持（support）值 = 静态守卫（边条件）与各前提事实条件的合取；
  - 事实条件 = 其所有支持值的析取；
  - 自底向上工作队列求不动点，天然处理依赖环。
- 增量采用 **DRed（delete / rederive）**：
  1. 删除被改/被删边登记的支持；
  2. 把波及到的事实（含依赖图上的传递消费者）**过度删除**清零；
  3. 用仍存在的支持重新求不动点。纯靠环内同伴互保、已无外部依据的循环
     在第 2 步被一起清除，第 3 步无法自我恢复；菱形删一条路径时另一条路径
     会被正确重导。这正是“仅引用计数会让环中旧可达性残留”的解法。
- `reference.py`：独立的**逐配置朴素遍历器**（不共享 BDD 表示），测试中
  对小图穷举全部配置与之逐配置比对。

## JSON 格式

```json
{
  "features": ["ssl", "legacy"],
  "packages": [
    {"id": "root", "version": "1.0", "symbols": ["main"],
     "exports": {"cli": "main", "old": null}}
  ],
  "edges": [
    {"from": "root", "to": "net", "kind": "public",
     "when": ["and", "ssl", ["not", "legacy"]]}
  ]
}
```

条件：特性名、`["not", x]`、`["and", ...]`、`["or", ...]`；省略 `when`
或 `true` 表示恒真，`false` 恒假。

## 库 API（可查询）

```python
from condexpose import PackageGraph, ExposureAnalyzer

analyzer = ExposureAnalyzer(PackageGraph.from_file("graph.json"))

analyzer.exposures()            # [ExposureRecord(name, source, condition)]
analyzer.exposure_condition("connect")
analyzer.conflicts()            # [Conflict(name, (srcA, srcB), common_condition)]
analyzer.witness("connect")                       # SupplyPath(配置+路径)
analyzer.conflict_paths(conflict)                 # 两条独立供应路径
analyzer.build_closure()                          # 仅构建闭包内可见
analyzer.runtime_packages() / analyzer.build_packages()

report = analyzer.delete_edge(edge_key)           # 增量删除
report = analyzer.modify_edge(edge_key, when)     # 增量改条件
# report.facts_overdeleted / facts_touched / packages_touched / bdd_nodes
```

## 演示与测试

```bash
python3 -m condexpose.demo.explain_conflict    # 冲突解释（共同配置+两条路径）
python3 -m condexpose.demo.library_usage       # 库查询 + 增量改边
python3 -m condexpose.demo.scale_report        # ROBDD 节点数 / 增量触及节点
python3 -m unittest condexpose.tests.test_analyzer -v
```

测试覆盖：小图穷举与逐配置遍历器比对、60 个随机小图穷举、冲突逐配置核对、
**环入口删除、菱形两路径删一条、别名合流、私有传递阻断、build 闭包不进入
运行暴露**、删除/改边与“全新构建”等价，以及特性数增长时的 ROBDD 节点数。

实际运行输出见 `results/`。

## 代码布局

- `condexpose/bdd.py`：ROBDD
- `condexpose/model.py`：包/边/来源数据模型与条件解析
- `condexpose/analyzer.py`：条件不动点 + DRed 增量 + 查询/路径取证
- `condexpose/reference.py`：独立逐配置参照遍历器
- `condexpose/tests/`：穷举、场景、随机、增量一致性、BDD 规模测试
- `condexpose/demo/`：示例图与三个可运行脚本
