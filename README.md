# matrixfold — 非交换序列折叠（2×2 整数矩阵）

按稳定排序键维护若干分组的矩阵序列，用平衡区间树（线段树）增量折叠出
每组的按序乘积，所有运算在指定模数 `m` 下进行。纯 Python 3.11 标准库，
测试使用 `unittest`。

## 设计要点

- **非交换**：内部节点存 `左子 * 右子`（顺序敏感），交换两个矩阵通常会
  改变乘积（见 `MatrixTest.test_multiplication_is_non_commutative`）。
- **不用逆矩阵**：矩阵可奇异（行列式 ≡ 0 mod m），删除中项通过重建树路径
  完成，绝不乘以逆元。
- **空组 = 单位矩阵**：空序列折叠为 `[[1,0],[0,1]]`。
- **稳定排序键**：组内按 `(sort_key, table, pk)` 排序，等键时以主键决胜，
  顺序确定可复现。
- **跨组原子更新**：一个事务先整体校验前后镜像，再统一落盘；跨组迁移
  （before/after 的 group 不同）与同事务其他变更一起原子生效，校验失败
  则整体拒绝、不留部分状态。
- **提交序号**：事务必须按严格递增的 `commit_seq` 应用；加载器会按
  `(commit_seq, tx_id)` 排序。
- **增量 = 全序扫描**：`product()`（线段树）与 `full_scan_product()`
  （朴素左到右扫描）在每个测试与演示的每个事务后逐一比对。

## 变更来源（本地夹具，不接消息平台）

`fixtures/changes.json`：

```json
{
  "modulus": 1000000007,
  "transactions": [
    {"tx_id": "tx-0001", "commit_seq": 1, "changes": [
      {"table": "items", "pk": "A", "before": null,
       "after": {"group": "g1", "sort_key": 10, "matrix": [[1, 1], [0, 1]]}}
    ]}
  ]
}
```

`before = null` 表示插入，`after = null` 表示删除，两者皆非空为更新
（group 不同即为跨组迁移）。

## 代码结构

- `matrixfold/matrix.py` — 2×2 矩阵（四元组）模 m 乘法、单位矩阵
- `matrixfold/segment_tree.py` — 平衡区间树：建树 O(n)、点更新 O(log n)、
  整体折叠 O(1)
- `matrixfold/group.py` — 分组：稳定排序、插删改、全序扫描参照实现
- `matrixfold/engine.py` — 事务引擎：前镜像校验、原子应用、提交序号
- `main.py` — 夹具加载与演示运行器
- `tests/test_matrixfold.py` — 20 个 unittest 用例

## 运行

```bash
python3 main.py                      # 使用 fixtures/changes.json
python3 main.py path/to/other.json
python3 -m unittest discover -s tests -v
```

## 运行样例

```
fixture=fixtures/changes.json modulus=1000000007 transactions=4

== tx tx-0001 (commit_seq=1, 5 change(s)) ==
  group 'g1': product=[[36, 36], [18, 18]] [OK vs full scan]
  group 'g2': product=[[2, 0], [0, 2]] [OK vs full scan]

== tx tx-0002 (commit_seq=2, 2 change(s)) ==
  group 'g1': product=[[18, 18], [6, 6]] [OK vs full scan]
  group 'g2': product=[[2, 4], [4, 8]] [OK vs full scan]

== tx tx-0003 (commit_seq=3, 2 change(s)) ==
  group 'g1': product=[[4, 4], [2, 2]] [OK vs full scan]
  group 'g2': product=[[6, 14], [12, 28]] [OK vs full scan]

== tx tx-0004 (commit_seq=4, 4 change(s)) ==
  group 'g1': product=[[1, 0], [0, 1]] [OK vs full scan]
  group 'g2': product=[[6, 14], [12, 28]] [OK vs full scan]
  group 'g3': product=[[2, 1], [3, 2]] [OK vs full scan]

all incremental results match full-order scans
```

样例覆盖：批量插入（含奇异矩阵 `[[1,2],[2,4]]`、`[[2,2],[2,2]]`）、
同组改键改值、跨组迁移（C: g1→g2）、删除中间奇异元素 B、g1 清空回到
单位矩阵、等排序键按主键决胜（g3 的 P、Q）。

## 测试结果

```
$ python3 -m unittest discover -s tests
....................
----------------------------------------------------------------------
Ran 20 tests in 0.200s

OK
```

其中 `test_randomized_incremental_matches_full_scan` 在模数 2、7、
10⁹+7 下各随机执行 300 个事务（随机插/删/改/跨组迁移，约 30% 奇异
矩阵），每个事务后逐组断言增量结果与全序扫描一致。
