"""可重放样例：覆盖查询、换乘规则、容量竞争、回滚、取消/更正、过期令牌与重启。

运行：python -m samples.replay   （或 python samples/replay.py）
输出为确定性文本，便于回放比对。
"""
from __future__ import annotations

import os
import sys
import tempfile

if __package__ in (None, ""):
    sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from transit.model import Graph, Timetable, fmt_time
from transit.query import QueryEngine
from transit.reserve import ReservationManager
from transit.planner import Planner


def build_timetable() -> Timetable:
    g = Graph()
    for v in ["A", "B", "C", "D", "E"]:
        g.add_vertex(v)
    g.add_vertex("X")                       # 孤立点
    g.add_edge("W-CD-1", "C", "D", 30)      # 多重边：两条 C->D 步行边
    g.add_edge("W-CD-2", "C", "D", 45)
    tt = Timetable(g)
    tt.set_transfer_time("B", 2)            # B 站换乘 2 分钟
    tt.set_transfer_time("C", 3)            # C 站换乘 3 分钟
    tt.add_trip("T1", ["A", "B"], [0, 5], [0, 5], capacity=2)
    tt.add_trip("T2", ["B", "C"], [6, 10], [6, 10], capacity=2)   # 5+2=7 > 6，赶不上
    tt.add_trip("T3", ["B", "C"], [7, 12], [7, 12], capacity=1)   # 正好可乘
    tt.add_trip("T5", ["C", "D"], [16, 26], [16, 26], capacity=1)  # 12+3<=16 可接; 15+3>16 不可接
    tt.add_trip("T5B", ["C", "D"], [16, 26], [16, 26], capacity=1) # 与 T5 同时刻并列
    tt.add_trip("T6", ["A", "C"], [1, 15], [1, 15], capacity=1)   # 直达 C 但更晚
    tt.add_trip("T4", ["C", "E"], [1430, 1510], [1430, 1510], capacity=2)  # 跨日 23:50->+1d 01:10
    return tt


def fmt_journey(j) -> str:
    parts = []
    for s in j.segments:
        if s.kind == "trip":
            parts.append(f"{s.trip_id}:{s.src}@{fmt_time(s.dep)}->{s.dst}@{fmt_time(s.arr)}")
        else:
            parts.append(f"walk({s.edge_id}):{s.src}->{s.dst}")
    return " | ".join(parts) + f"  到达={fmt_time(j.arrival)} 换乘={j.transfers}"


def run(state_dir: str | None = None) -> list[str]:
    lines: list[str] = []
    out = lines.append
    tt = build_timetable()
    engine = QueryEngine(tt)
    state_dir = state_dir or tempfile.mkdtemp(prefix="transit-replay-")
    state_path = os.path.join(state_dir, "reservations.json")
    mgr = ReservationManager(tt, state_path=state_path, now=0)
    planner = Planner(tt, engine, mgr)

    out("== 图结构 ==")
    out(f"顶点: {sorted(tt.graph.vertices)}  孤立点: {tt.graph.isolated_vertices()}")
    out(f"C->D 平行边: {[e.edge_id for e in tt.graph.out_edges('C')]}")

    out("== 查询1: A->C 自 00:00 (<=1 次换乘) ==")
    for j in engine.query("A", "C", 0, max_transfers=1):
        out("  " + fmt_journey(j))
    out("  规则: 到站B=00:05, 换乘2分 -> 00:06发车不可乘, 00:07发车可乘")

    out("== 查询2: A->D 自 00:00 (<=2 次换乘, 三条非支配路线) ==")
    journeys_ad = engine.query("A", "D", 0, max_transfers=2)
    for j in journeys_ad:
        out("  " + fmt_journey(j))

    out("== 查询3: C->E 自 23:20 (跨日班次) ==")
    for j in engine.query("C", "E", 1400, max_transfers=0):
        out("  " + fmt_journey(j))

    out("== 查询4: C->D 自 00:00 (同标签并列旅程) ==")
    for j in engine.query("C", "D", 0, max_transfers=0):
        out("  " + fmt_journey(j))

    out("== 容量预留 ==")
    token1 = mgr.reserve(["T1", "T3", "T5"], ttl=100)
    out(f"请求1 预留 T1+T3+T5: {'成功 ' + token1 if token1 else '失败'}")
    token2 = mgr.reserve(["T1", "T3"], ttl=100)
    out(f"请求2 竞争 T3 最后座位: {'成功' if token2 else '失败(座位已占)'}")
    before = mgr.available("T2")
    token3 = mgr.reserve(["T2", "T3"], ttl=10)
    after = mgr.available("T2")
    out(f"请求3 两段预留(T2+T3)部分失败: {'已回滚' if token3 is None else '意外成功'}"
        f", T2 额度 {before}->{after} (无泄漏)")

    out("== 确认旅程与证据失效 ==")
    j_ad = next(j for j in journeys_ad if j.trip_ids == ["T1", "T3", "T5"])
    cj1 = planner.confirm(j_ad, token1)
    j_ac = engine.query("A", "C", 1, max_transfers=0)[0]   # T6 直达
    token6 = mgr.reserve(["T6"], ttl=100)
    cj2 = planner.confirm(j_ac, token6)
    out(f"确认后: J1={cj1.status.value} J2={cj2.status.value}")
    tt.update_trip("T6", departures=[2, 16], arrivals=[2, 16])   # 更正
    out(f"更正 T6 后: J1={cj1.status.value} J2={cj2.status.value}")
    tt.cancel_trip("T3")                                          # 取消中途班次
    out(f"取消中途班次 T3 后: J1={cj1.status.value} J2={cj2.status.value}")
    alt = engine.query("A", "D", 0, max_transfers=2)
    out(f"重规划 A->D: 可行旅程数={len(alt)}")

    out("== 过期与代际令牌 (有效期左闭右开) ==")
    token_e = mgr.reserve(["T2"], ttl=50)
    mgr.advance(49)
    out(f"t=49 令牌有效: {mgr.is_valid(token_e)}")
    mgr.advance(50)
    out(f"t=50 令牌已过期: {not mgr.is_valid(token_e)}")
    cap_before = mgr.available("T2")
    ok = mgr.release(token_e)
    out(f"过期令牌释放: {'成功' if ok else '被拒绝'}, T2 额度变化: {mgr.available('T2') - cap_before}")

    out("== 重启后的过期令牌 ==")
    token_r = mgr.reserve(["T4"], ttl=60)   # now=50, 有效期 [50, 110)
    out(f"重启前预留 T4: {token_r}")
    mgr2 = ReservationManager(tt, state_path=state_path, now=200)
    cap = mgr2.available("T4")
    ok = mgr2.release(token_r)
    out(f"重启后: T4 可用额度={cap}(已回收且仅一次), 过期令牌释放: {'成功' if ok else '被拒绝'}")

    out("== 已确认旅程状态 ==")
    for line in planner.report():
        out("  " + line)
    return lines


def main() -> None:
    for line in run():
        print(line)


if __name__ == "__main__":
    main()
