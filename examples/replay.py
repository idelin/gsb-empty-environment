"""可重放样例：python -m examples.replay

固定操作序列，覆盖：半开区间冲突、软撤销决策、push/pop、
批量更正与同 ID 版本、零权环、大整数、参考核对。
"""
from temporal_diff import ConstraintSystem, check_window, format_pieces


def replay() -> str:
    log = []
    emit = log.append
    cs = ConstraintSystem()

    emit("== step 1: build graph (vertices x y z w, w/z zero-weight cycle) ==")
    for v in ("x", "y", "z", "w"):
        cs.add_vertex(v)
    cs.add_constraint("c1", "x", "y", -1, 0, 10, priority=1)   # y - x <= -1 on [0,10)
    cs.add_constraint("c2", "y", "x", -1, 5, 15, priority=2)   # x - y <= -1 on [5,15)
    cs.add_constraint("c3", "z", "w", 0, 0, 20, priority=1)    # w - z <= 0
    cs.add_constraint("c4", "w", "z", 0, 0, 20, priority=1)    # z - w <= 0 (零权环，可行)

    emit("== step 2: query [0, 20) -> conflict only on overlap [5, 10) ==")
    emit(format_pieces(cs.query(0, 20)))

    emit("== step 3: push, soft-revoke lowest priority, explicit decisions ==")
    cs.push()
    for d in cs.resolve(0, 20):
        emit(f"  {d}")
    emit(format_pieces(cs.query(0, 20)))

    emit("== step 4: pop restores domain, evidence and priority choices ==")
    cs.pop()
    emit(f"  revoked={sorted(cs.revoked)} decisions={len(cs.decisions)}")
    emit(format_pieces(cs.query(0, 20)))

    emit("== step 5: batch correct c2 -> [10, 15), version bumps, conflict gone ==")
    cs.batch_correct([("upsert", "c2", "y", "x", -1, 10, 15, 2)])
    emit(f"  c2 version = {cs.constraints['c2'].version}")
    emit(format_pieces(cs.query(0, 20)))

    emit("== step 6: big integers on [30, 40) ==")
    big = 10**40
    cs.batch_correct([("upsert", "big1", "x", "w", -big, 30, 40, 1)])
    emit(format_pieces(cs.query(30, 40)))

    emit("== step 7: per-segment full Bellman-Ford reference cross-check ==")
    problems = check_window(cs, 0, 50)
    emit("  REFERENCE OK" if not problems else "  REFERENCE FAIL: " + "; ".join(problems))
    return "\n".join(log)


if __name__ == "__main__":
    print(replay())
