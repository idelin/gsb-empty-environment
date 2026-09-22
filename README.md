# 增量视图维护（IVM）

纯 Python 3.11 标准库实现，不调用任何 SQL 引擎。视图由 **过滤 Filter、
投影 Project、等值连接 Join、分组 GroupBy** 组合而成；输入是基础关系上
**带行 ID 的插入/删除**，算子间以 `(元组, 重数差量)` 传播，视图终点物化
`元组 -> 重数` 并记录差量日志。

## 语义约定

- **多重集合**：同一元组可有多份，重数计数；投影把重复行折叠为重数。
- **负重数即错误**：任何元组重数变负抛 `NegativeMultiplicityError`；
  删除不存在的行 ID 抛 `UnknownRowError`，重复插入行 ID 抛 `DuplicateRowError`。
- **连接**：一侧差量 `m` 与另一侧每个匹配行（重数 `om`）产生 `m*om` 的输出
  差量；撤回最后支撑行时输出行被删除。
- **分组**：输出 `(键..., count, sum)`；计数变化输出 撤回旧行+插入新行，
  计数归零时组行被撤回。
- **NULL（None）**：比较谓词遇 NULL 为假；连接键为 NULL 不匹配；
  分组中 NULL 自成一组；求和忽略 NULL（计数仍计入）。
- **批次顺序无关**：差量按重数加减合并，交换批次顺序最终视图一致。

## 文件

- `ivm.py` — 引擎（Relation / Filter / Project / Join / GroupBy / View）
- `demo.py` — 运行样例：emp ⋈ dept 按部门统计人数与工资和
- `test_ivm.py` — unittest 测试（17 例）

## 运行

```sh
python3.11 demo.py        # 运行样例
python3.11 -m unittest -v # 运行测试
```
