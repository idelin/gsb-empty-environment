# vtline — 版本化有效时间轴

纯 Python 3.11 标准库实现：版本化有效时间（valid-time）时间轴，生成最少连续
变化段的可逆差量，支持三方合并、重定位与按身份撤回更正，全部结果用原始端点
全量扫描核验。

## 模块

- `vtline/time_model.py` — 精确时间模型：`WallClock`（墙钟时间）、`Instant`
  （时间点）、`Duration`（持续时长）三种类型互不混用；整数纳秒精确表示；
  `OffsetTable` 合成偏移转换表负责墙钟时间 ⇔ 时间点换算，跳过段报
  `InvalidWallTimeError`，重复段报 `AmbiguousWallTimeError`。
- `vtline/model.py` — `Entry`（身份 + 值）、`Segment`、`ChangeSpan`、`Diff`；
  变化类型区分 `ADD` / `DELETE` / `MODIFY`。
- `vtline/timeline.py` — 不可变持久化时间轴；`apply()` 先验证基线摘要
  （SHA-256 摘要），未改区间与旧快照共享节点，`ApplyStats` 记录实际复制
  节点数。
- `vtline/diff.py` — `diff` 生成最少连续变化段（同值相邻段合并，不同变更
  类型不合并，零长度变化不输出）；`inverse` 逆差量；`compose` 组合。
- `vtline/merge.py` — `merge_three_way` 三方合并两个独立分支差量，区间重叠
  且改值不兼容时输出最小冲突段；`rebase` 把差量重定位到新基线。
- `vtline/repository.py` — 版本链；`retract(identity)` 按身份撤回最近一次
  更正（恢复更正前的值），不按当前值匹配，不删除事实本身。
- `vtline/verify.py` — 原始端点全量扫描核验（`endpoints_of` /
  `full_scan_equal`）。

## 运行

```console
$ python3.11 demo.py
== 1. versioned valid-time timeline: add / correct / delete ==
  commit#1 [08:00:00,12:00:00) add    '-' -> 20.5
  commit#1 [14:00:00,18:00:00) add    '-' -> 1013
  commit#1 nodes: copied=2 shared=0
  commit#2 [08:00:00,10:00:00) modify 20.5 -> 21.0
  commit#2 [16:00:00,18:00:00) delete 1013 -> '-'
  commit#2 nodes: copied=3 shared=0

== 2. reversible apply + baseline summary verification ==
  inverse(d2) restores commit#1: True
  baseline digest verified on apply: a245fbf1f3d213ff...
  undo nodes: copied=2 shared=0

== 3. composition verified by full scan over original endpoints ==
  apply(compose(d1,d2)) == apply(d1)->apply(d2): True
  composed spans: 3 (vs 2+2 sequential)

== 4. three-way merge of two independent branch diffs ==
  conflicts: 1
  conflict [10:00:00,11:00:00) base=20.5 left=22.5 right=23.0
  merged diff replays merged timeline: True

== 5. rebase a branch diff onto an independent new base ==
  conflicts during rebase: 0
  rebased diff applies cleanly to new base: True
  sensor-d (unrelated) preserved: True

== 6. retraction of a correction, by identity ==
  before retract: sensor-a = 99.9  sensor-b = 99.9
  after retract:  sensor-a = 20.5  sensor-b = 99.9  (same value, different identity: untouched)
```

## 测试

```console
$ python3.11 -m unittest discover -s tests -v
...
----------------------------------------------------------------------
Ran 44 tests in 0.016s

OK
```

44 个用例覆盖：时间模型（精确算术、类型区分、偏移表跳过/重复段）、差量
分类与最段合并规则、零长度变化抑制、基线摘要校验、可逆应用、组合与顺序
应用等价（全量扫描）、三方合并与最小冲突段、重定位、按身份撤回更正、
旧快照结构共享与复制节点计数。
