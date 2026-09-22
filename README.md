# minirel — 反向列裁剪与裁剪证据（纯标准库，不调用 SQL 引擎）

从查询输出反向推导每个扫描节点的必需列：过滤谓词、连接键、排序键、
投影表达式、聚合参数、分组键都是**隐式依赖**，即使不出现在输出中也会
被保留；其余列在 Scan 处裁掉，并输出逐列的裁剪证据（KEEP/PRUNE 及原因）。

## 语义约定（显式）

- **NULL**：`None`。比较/算术遇 NULL 得 UNKNOWN；过滤只保留 TRUE；
  等值连接中 NULL 键永不匹配（NULL ≠ NULL）；`COUNT(col)` 跳过 NULL，
  `COUNT(*)` 计行数；分组时 NULL 键归入同一组。
- **多重集合（bag）**：重复行全程保留，连接产生全部匹配对（基数相乘），
  无任何去重。
- **排序**：稳定排序；ASC 时 NULL 排最前，DESC 时 NULL 排最后。
  排序键不在输出中时，执行仍保留到排序完成后再丢弃。
- **COUNT(*)**：不需要任何列。Scan 可发射零宽元组（每行一个 `()`），
  行重数（multiplicity）不因裁剪丢失，计数不会退化成空表。
- **同名列**：列以 `ColRef(source, name)` 限定来源别名，`e.name` 与
  `m.name` 是自连接中两个不同的列；未加区分的重名连接会直接报错。

## 结构

- `minirel/algebra.py` — 表（bag of tuples）、NULL、三值逻辑表达式
- `minirel/operators.py` — Scan/Filter/Join/Project/Sort/Aggregate，
  `analyze()` 反向需求传播 + `execute()` 裁剪后执行 + `Evidence`
- `demo.py` — 本地合成关系表与三条查询的运行样例
- `test_minirel.py` — 17 个 unittest 用例

## 运行

```sh
python3 demo.py          # 运行样例（含裁剪证据）
python3 -m unittest -v   # 测试
```

## 运行样例（`python3 demo.py` 节选）

Q1：输出不含排序列 salary，但证据显示其被保留到排序后：

```
[n1] scan employees AS e
  KEEP  e.dept_id    <- join key at n3
  KEEP  e.name       <- project 'name' at n6
  KEEP  e.salary     <- filter predicate at n4; sort key at n5 (dropped after sort)
  PRUNE e.emp_id     (unreferenced by output, filter, join, sort, or aggregate)
  ...
notes:
  - n5: sort key column(s) e.salary retained through sort, dropped afterwards (not in query output)
result schema: ['name', 'dept']
   ('amy', 'eng')
   ('bob', 'eng')
```

Q2：`COUNT(*)` 不需要任何列，行重数经零宽元组保留，结果非空表：

```
notes:
  - n9: COUNT(*) needs no columns; row multiplicity preserved via zero-width tuples
result schema: ['n']
   (3,)
```

Q3：自连接同名列按来源区分（`e.name` → employee，`m.name` → manager），
重复行 `(cy, amy)` 出现两次（多重集合语义）。

## 测试结果

```
Ran 17 tests in 0.001s
OK
```
