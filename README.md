# 动态多组线段最近点对查询

Python 3.11，仅标准库（`fractions` / `heapq` / `unittest`）。

## 结构

- `geometry.py` — 精确有理数几何：方向判定、点-段/段-段平方距离，
  输出精确参数 `t`；退化情形有明确定义（零长段即点、共线重叠取字典序
  最小参数、平行等距连续解取规范代表），全程无 epsilon。
- `scene.py` — 持久化（path-copying）AVL 包围树：
  - 局部移动只重建受影响路径上的节点（O(height)），其余子树与旧版本共享；
  - 重平衡只创建新节点，在途查询持有不可变 `Version`，不可能访问已回收节点；
  - `Node.live` / `Node.allocated` 资源计数，配合 `gc` 证明旧版本释放；
  - 查询为 best-first 双树遍历，支持排除关系（组级/场景级）、半径阈值
    （含等于阈值）、`all`（全部并列证据）与 `representative`（稳定代表）模式、
    协作式取消（`Query.cancel` / `CancelToken` → `QueryCancelled`）。
- `test_closest_pair.py` — 17 个 unittest 用例。

## 测试覆盖

- 交叉距离 0 且参数精确为 1/2；共线重叠规范参数；有理坐标精确距离。
- 平行等距多解：`all` 模式返回全部并列证据，`representative` 模式返回
  稳定代表（按段 ID 字典序，与查询次数无关）。
- 半径阈值过滤（阈值含边界）；排除关系生效。
- 删除最近段后找到次近候选；共享端点证据去重（每个无序对仅一次）；
  零长段作为点参与查询。
- 持久树：局部移动只修复路径节点（断言新节点数 ≤ 8·height+8 且共享子树
  非空）；重平衡期间在途查询结果与暴力枚举一致，且查询存活期间旧节点
  不被回收。
- 事件交错（固定种子 20260925，160 步随机 add/move/remove/排除/快照/
  取消/历史版本查询）全部与全对枚举对照一致。
- 资源计数：`Node.live` 在删除快照/取消查询/销毁场景后精确回落到
  可达节点数，证明旧版本正确释放。

## 实际运行结果

命令：`python3.11 -m unittest test_closest_pair -v`

```
test_interleaved_events_match_bruteforce ... ok
test_collinear_overlap_canonical_params ... ok
test_crossing_distance_zero_exact_params ... ok
test_rational_coordinates_exact ... ok
test_touching_endpoint_distance_zero ... ok
test_zero_length_segment_is_point ... ok
test_local_move_repairs_only_path_nodes ... ok
test_rebalance_keeps_inflight_query_safe ... ok
test_cancelled_query_releases_version ... ok
test_old_versions_released ... ok
test_exclusion_relation ... ok
test_parallel_equidistant_all_mode ... ok
test_parallel_equidistant_representative_mode_is_stable ... ok
test_radius_threshold_filters ... ok
test_delete_nearest_reveals_next_candidate ... ok
test_shared_endpoint_not_duplicated ... ok
test_zero_length_segment_as_point_in_scene ... ok

----------------------------------------------------------------------
Ran 17 tests in 4.276s

OK
```

复跑两次（1.801s / 2.576s）均为 OK，结果确定。
