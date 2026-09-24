# hyperproj — 分层图投影引擎

纯 Python 3.11 标准库实现：把分组内的节点折叠成超节点、汇总跨组边为超边，
同时保留完整的原始边证据，支持嵌套展开、原子移动、撤销、命名快照与相互
独立的折叠视图。

## 设计要点

- **数据模型**：顶点、边、组全部使用字符串 ID；保留孤立点与多重边；
  边带左闭右开时间区间 `[start, end)`（`None` 表示开口端）。
- **折叠语义**：`collapsed` 集合始终保持反链（折叠外组时自动收起其已折叠
  后代）。顶点的可见节点 = 离根最近的已折叠祖先，否则是顶点自身。
- **组内边不造假**：两端点落在同一可见节点上的边进入 `internal` 索引，
  永远不会以 `(G, G)` 的形式出现在跨组超边统计里。
- **增量索引**：单点移动只访问该点的祖先链（嵌套成员计数）与其关联边
  （超边重数 + 原始边反查 `edge_location`）；折叠/展开/组移动只触及对应
  子树的顶点及其关联边。
- **原子批次**：`apply(...)` 先验证整批（组层次成环、ID 冲突、未知 ID、
  非法区间），任一失败整批回滚；每个成功批次压入撤销栈。
- **多视图与快照**：`fork()` 生成互不影响的独立视图；`save_snapshot` /
  `restore` 提供历史快照（restore 本身可 undo）。

## 运行

```bash
python3 demo.py                              # 运行样例
python3 -m unittest discover -s tests -v     # 单元 + 随机差分测试
```

## 目录结构

- `hyperproj/model.py` — `Edge` 与批次操作（`AddVertex` / `Collapse` / ...）
- `hyperproj/projection.py` — 增量投影引擎 `Projection`
- `hyperproj/brute.py` — 独立暴力重投影，作为测试/演示的对照 oracle
- `tests/test_projection.py` — 语义单元测试
- `tests/test_random.py` — 随机展开/移动/撤回 vs 重新投影的差分测试
- `demo.py` — 可运行样例
