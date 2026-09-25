# 动态层次包围盒索引

纯 Python 3.11 标准库实现的动态 BVH/AABB 索引，支持任意正整数维，使用 `int` 和 `fractions.Fraction` 保持精确拓扑判断，不使用浮点 epsilon。

## 语义

- 包围盒按轴表示为 `[(lo0, hi0), ...]`，要求每个区间 `lo <= hi`；点、线段、平面等退化盒均可表示。
- 两个闭区间盒在所有轴上都有交集时相交，边界刚好接触也算相交。
- `intersects(query)` 返回与查询盒相交的对象 ID，并按 ID 排序。
- `nearest(point)` 返回最近对象 ID；`nearest_k(point, k)` 返回 `(ID, 平方距离)`。
- 最近距离比较平方距离；距离并列时按 ID 升序。
- 空树的相交和最近查询分别返回 `[]` 和 `None`，不抛异常。
- `move` 会先从旧包围区域摘出叶子，再按新区域插入；移动后旧区域查询不会返回该对象。
- `insert`、`remove`、`discard`、`move` 后都会沿祖先重算包围盒；`check_invariants()` 可验证每个内部节点严格等于两个子盒的并集。
- 插入使用确定性的 SAH 词典序：几何代价相同依次比较目标高度和内部节点 ID；树过高时按最长轴中心和 ID 做确定性平衡重建。

## 快速使用

```python
from fractions import Fraction
from bvh import BVHIndex

tree = BVHIndex(dimension=2)
tree.insert(1, [(Fraction(1), Fraction(2)), (0, 1)])
tree.insert(2, [(0, 1), (0, 1)])

print(tree.intersects([(1, 1), (0, 0)]))       # [1, 2]，接触算相交
print(tree.nearest_k((Fraction(5, 2), 0), 2)) # [(1, 1/4), (2, 9/4)]

tree.move(1, [(10, 11), (10, 11)])
print(tree.intersects([(1, 1), (0, 0)]))       # [2]
```

## API

- `BVHIndex(dimension=2)`：创建固定维数的索引。
- `insert(item, box)`：插入；重复 ID 抛出 `KeyError`。
- `remove(item)`：删除并返回旧 `Bounds`；不存在时抛出 `KeyError`。
- `discard(item)`：删除；不存在时返回 `None`。
- `move(item, box)`：移动；新旧盒相同是定义好的 no-op。
- `intersects(box)` / `query_intersection(box)`：相交查询。
- `nearest(point)`：最近 ID，空树为 `None`。
- `nearest_with_distance(point)`：最近 `(ID, squared_distance)`。
- `nearest_k(point, k)`：最多 `k` 个最近结果，按 `(distance, ID)` 排序。
- `bounds(item)`、`ids()`、`len(tree)`、`item in tree`、`check_invariants()`。

## 运行

```bash
python3.11 example.py
python3.11 -m unittest -v
```

实测样例输出：

```text
empty intersection: []
empty nearest: None
touching intersection: [1, 2]
nearest k=3: [(1, Fraction(1, 4)), (2, Fraction(9, 4)), (3, Fraction(41, 2))]
old region after move: [2]
new region after move: [1]
ids after remove: [1, 2]
invariants: OK
```

实测测试结果：

```text
test_degenerate_boxes_and_boundary_touching ... ok
test_dynamic_insert_remove_sequence_against_brute_force ... ok
test_empty_tree_queries_are_defined ... ok
test_invalid_exact_coordinates_and_shapes ... ok
test_missing_duplicate_and_invalid_operations ... ok
test_move_during_rebuild_does_not_reattach_old_leaf ... ok
test_move_removes_old_region_and_keeps_invariants ... ok
test_nearest_tie_is_sorted_by_id ... ok
test_rational_coordinates_without_float_epsilon ... ok
test_rebuild_forces_logarithmic_height ... ok

----------------------------------------------------------------------
Ran 10 tests in 0.924s

OK
```

## 文件

- `bvh.py`：BVH 数据结构、精确盒运算、动态维护和查询。
- `test_bvh.py`：`unittest` 测试，包括退化盒、有理数、移动、删除、并列和不变量。
- `example.py`：可直接运行的演示。
