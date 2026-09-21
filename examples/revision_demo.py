"""修订消费样例：演示补录迟到请求、调整配置后如何消费 RevisionRange。

运行：python3 examples/revision_demo.py
"""

import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from token_bucket import TokenBucketEngine


def show(title, engine, highlight_ids=()):
    print(f"\n== {title} ==")
    print(f"{'id':>3} {'time':>5} {'seq':>3} {'before':>7} {'after':>7} "
          f"{'decision':>8} {'ver':>4}  reason")
    for r in engine.results():
        mark = "*" if r.request_id in highlight_ids else " "
        decision = "PASS" if r.allowed else "REJECT"
        print(f"{mark}{r.request_id:>3} {str(r.time):>5} {r.seq:>3} "
              f"{str(r.level_before):>7} {str(r.level_after):>7} "
              f"{decision:>8} {r.version:>4}  {r.reason}")


def show_range(label, rng):
    if rng.is_empty:
        print(f"{label}: 无受影响判定（{rng.note}）")
    else:
        print(f"{label}: 重算下标 [{rng.start}, {rng.end}]，"
              f"涉及请求 id={list(rng.recomputed_ids)}，"
              f"提前停止={rng.stopped_early}")


def main():
    # 场景 1：容量 2、每毫秒补 1，0 时刻三次请求只有前两次通过
    engine = TokenBucketEngine([(0, 2, 1)])
    engine.add_request(0, 1)
    engine.add_request(0, 2)
    engine.add_request(0, 3)
    engine.add_request(10, 0)
    show("初始回放：容量 2、1 token/ms", engine)

    # 场景 2：补录一条迟到的更早请求 (t=0, seq=0)
    before = {r.request_id: r for r in engine.results()}
    _, rng = engine.add_request(0, 0)
    show_range("\n补录 (t=0, seq=0) 后的修订范围", rng)
    show("补录后的完整状态", engine, highlight_ids=rng.recomputed_ids)
    kept = [r for r in engine.results() if r.request_id not in rng.recomputed_ids]
    print("范围外保持原身份的结果：",
          [(r.request_id, r.version, r is before[r.request_id]) for r in kept])

    # 场景 3：从 t=5 起把容量缩到 1（分段配置调整）
    rng = engine.replace_config([(0, 2, 1), (5, 1, 1)])
    show_range("\n容量从 t=5 起缩为 1 后的修订范围", rng)
    show("配置调整后的完整状态", engine, highlight_ids=rng.recomputed_ids)

    # 场景 4：分数速率 + 长时间空闲封顶（无浮点漂移）
    frac = TokenBucketEngine([(0, Fraction(3, 2), Fraction(1, 3))])
    frac.add_request(0, 0)
    frac.add_request(9000, 0)
    show("分数速率 1/3、容量 3/2、空闲 9000ms 后封顶", frac)
    exact = frac.results()[1].level_before
    print(f"t=9000 事前水位 = {exact}（精确 Fraction，{float(exact)}）")


if __name__ == "__main__":
    main()
