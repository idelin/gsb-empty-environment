# transit — 离散班次表的帕累托旅程查询与容量预留

Python 3.11，仅标准库，测试基于 `unittest`。

## 语义约定

- 时间为整数分钟计数器，可超过 1440（跨日班次）。
- 时间区间左闭右开 `[start, end)`：发车时刻 `d` 在 `t0 == d` 时可搭乘；
  预留在 `[created, expires_at)` 内有效。
- 换乘规则：在车站下车时刻 `a` 后，下一班次需满足
  `dep >= a + transfer_time[station]`；步行至邻站再上车需
  `dep >= a + walk + transfer_time[dst]`；查询首次上车无换乘惩罚。
- 旅程按 `(到达时刻, 换乘次数)` 比较，返回全部非支配代表旅程。
- 顶点与边均为字符串 ID；保留孤立点，允许平行多重边（步行取最短路）。

## 结构

- `transit/model.py` — `Network`（多重图）、`Trip`/`Stop`、`Timetable`
  （版本化，任何班次变更递增 `version`）、`Journey`/`RideLeg`/`WalkLeg`。
- `transit/query.py` — `pareto_query`（标签式帕累托搜索）与
  `brute_force_pareto_pairs`（显式时间展开图穷举，作为独立校验 oracle）。
- `transit/reservation.py` — 原子多段预留：任一段失败即回滚全部已占段；
  预留以 `(id, generation)` 代际令牌标识，释放/过期会递增代际，旧令牌
  无法再次返还额度；`snapshot`/`restore` 持久化，重启后过期令牌仍无效。
- `transit/planner.py` — 门面：查询证据按班次表版本缓存，班次取消/更正
  使相关证据失效；依赖被更新班次的已确认旅程转为 `replan` 状态。
- `replay.py` — 可重放样例（确定性输出，含断言自检）。
- `tests/test_transit.py` — 全部 unittest。

## 运行

```sh
python3.11 -m unittest discover -s tests -t . -v
python3.11 replay.py
```

## 测试结果

```
Ran 15 tests in 0.007s

OK
```

覆盖：到站 5 换乘 2 赶不上 6 发车可搭 7（左闭右开边界）；同发车时刻两条
非支配路线；跨日班次；孤立点与多重边；两请求竞争最后座位仅一个成功；
部分段预留失败回滚无额度泄漏；过期令牌（含 JSON 快照模拟重启后）无法
释放新额度；撤销中途班次触发 `replan` 且证据失效；班次更正后结果更新；
8 个随机种子 × 6 查询与小规模时间展开穷举逐一比对一致（另以 60 种子
480 查询压力校验通过）。

`replay.py` 输出末行 `REPLAY OK` 表示全部场景断言通过。
