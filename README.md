# 离散班次表帕累托旅程查询与容量预留

Python 3.11+，仅标准库。从离散班次表实现**最早到达 × 最少换乘**的帕累托旅程查询，
以及带代际令牌的班次容量原子预留。

## 结构

- `transit/model.py` — 图（顶点/边均为字符串 ID，保留孤立点与多重边）、班次表
  （每站换乘时间、班次容量、跨日运行、取消 `cancel_trip` 与更正 `update_trip`）。
  时间为整数分钟，跨日用 ≥1440 表示；所有时间区间左闭右开 `[start, end)`。
- `transit/query.py` — 查询引擎：按乘车次数分轮的连接扫描（CSA）求帕累托前沿
  `(到达时刻, 换乘数)`，再用带预算的时间展开 DFS 重构具体旅程（同标签并列旅程均保留）。
  换乘规则：到站 `a` 后可乘 `dep >= a + transfer_time(stop)` 的其他班次；同班次续乘不限；
  起点首次上车不限换乘时间。
- `transit/bruteforce.py` — 小规模时间展开穷举（多标签 Dijkstra），用于交叉验证。
- `transit/reserve.py` — 容量预留：多段两段式原子提交，任一段失败回滚全部暂存段；
  令牌 `<rid>#<gen>`，过期/释放时代际递增，旧令牌释放被拒绝且不增加额度；
  状态持久化 JSON，重启后过期预留只回收一次。
- `transit/planner.py` — 已确认旅程记录 `(trip_id, version)` 证据；班次取消/更正后
  状态置为 `REPLAN_REQUIRED`。
- `samples/replay.py` — 可重放样例（确定性输出，见 `samples/transcript.txt`）。
- `tests/test_transit.py` — unittest 套件。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 测试
python3 -m samples.replay                  # 重放样例
```

## 样例覆盖

- 到站 5、换乘 2：不能搭 6 发车，可搭 7 发车；
- 同出发时刻两条非支配路线（直达更晚 vs 换乘更早）与同标签并列旅程（T5/T5B）；
- 跨日班次（23:50 → 次日 01:10）；孤立点与 C→D 平行步行边；
- 两请求竞争最后座位仅一个成功；部分段预留失败回滚无额度泄漏；
- 撤销中途班次 / 更正班次 → 已确认旅程输出 `REPLAN_REQUIRED`；
- 预留有效期 `[t, t+ttl)`：t=49 有效、t=50 过期；过期令牌与重启后的过期令牌
  释放均被拒绝且额度不增加；
- 随机小规模实例上查询结果与时间展开穷举一致（测试 80 例，另验证 2000 例零偏差）。
