# 网络路径容量预留系统

Python 3.11，仅标准库，测试使用 unittest。

## 功能

- 固定需求列表按 **(优先级降序, 需求ID升序)** 顺序预留路径容量
- 容量按时间区间核算，区间 **左闭右开** `[start, end)`（首尾相接不冲突）
- 顶点、边均为字符串 ID；**保留孤立点**，允许 **多重边**
- 边故障时 **仅重路由受影响需求**；重路由失败 **回滚到原预留**
- 替代路径逐边分配，**中途失败释放已占用边**（原子性）
- 边恢复 **幂等**：未故障时为空操作，不会重复增加可用容量
- 容量只够一条需求时，只有一条被承诺成功

## 结构

- `network_reservation/model.py` — Interval / Edge / Graph / Demand / Reservation
- `network_reservation/manager.py` — ReservationManager（预留、寻路、故障、恢复）
- `main.py` — 运行样例
- `tests/test_reservation.py` — 19 个单元测试

## 运行

```bash
python3.11 main.py                          # 运行样例
python3.11 -m unittest discover -s tests -v # 运行测试
```
