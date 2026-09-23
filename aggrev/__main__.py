"""End-to-end sample: fixtures -> upstream corrections -> version-DAG
application downstream (with out-of-order + duplicate delivery) -> ack ->
garbage collection -> independent verification."""
from __future__ import annotations

from pathlib import Path

from .downstream import Downstream
from .fixtures import load_events
from .gc import collect
from .publisher import Publisher
from .upstream import Upstream
from .verifier import verify

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"


def fmt_value(value: dict) -> str:
    return str(value) if value else "{}"


def main() -> int:
    events_a = load_events(FIXTURES / "source_a.json")
    events_b = load_events(FIXTURES / "source_b.json")
    print("=== 1. 加载本地夹具（事务ID/表/主键/前后值/提交序号） ===")
    print(f"  source A: {len(events_a)} 个变更事件, source B: {len(events_b)} 个变更事件")

    publisher = Publisher()
    downstream = Downstream("ds-1")
    up_a = Upstream("A", publisher)
    up_b = Upstream("B", publisher)

    print("\n=== 2. 多上游发布更正（版本依赖形成 DAG） ===")
    up_a.refresh()
    c_a1 = up_a.publish_batch(events_a[0:3])
    print(f"  发布 {c_a1.revision_id}: parents={list(c_a1.parents)} deltas={c_a1.deltas}")
    # B 未刷新，仍基于 V0 -> 与 A-r1 形成分支
    c_b1 = up_b.publish_batch(events_b[0:2])
    print(f"  发布 {c_b1.revision_id}: parents={list(c_b1.parents)} deltas={c_b1.deltas}  (与 A-r1 并行的分支)")
    up_a.refresh()
    c_a2 = up_a.publish_batch(events_a[3:6])
    print(f"  发布 {c_a2.revision_id}: parents={list(c_a2.parents)} deltas={c_a2.deltas}  (合并两个分支)")
    up_b.refresh()
    c_b2 = up_b.publish_batch(events_b[2:4])
    print(f"  发布 {c_b2.revision_id}: parents={list(c_b2.parents)} deltas={c_b2.deltas}")
    print(f"  发布方当前值: {fmt_value(publisher.value())}, heads={publisher.heads()}")

    print("\n=== 3. 下游按版本链应用（乱序 + 重复投递） ===")
    delivery = [
        (c_a2, "乱序：父版本还没到"),
        (c_b1, ""),
        (c_a1, "补齐缺口"),
        (c_a1, "重复投递同一 ID"),
        (c_b2, ""),
    ]
    for correction, note in delivery:
        result = downstream.receive(correction)
        line = (
            f"  receive {correction.revision_id:<5} -> {result.status:<9}"
            f" value={fmt_value(downstream.value())}"
        )
        if result.detail:
            line += f"  ({result.detail})"
        if result.resumed:
            line += f"  [缺口补齐，恢复应用: {result.resumed}]"
        if note:
            line += f"  # {note}"
        print(line)
    print(f"  下游 checkpoint: {downstream.checkpoints()}")

    print("\n=== 4. 确认 + checkpoint 原子关联，旧修订垃圾回收 ===")
    publisher.ack("ds-1", downstream.acked_versions(), downstream.checkpoints())
    print(f"  下游确认版本: {downstream.acked_versions()}")
    collected = collect(publisher)
    print(f"  回收修订: {collected}")
    print(
        f"  回收后: base={fmt_value(publisher.base())}, "
        f"存活修订={sorted(publisher.store.state['revisions'])}, "
        f"值不变={fmt_value(publisher.value())}"
    )

    print("\n=== 5. 独立从原始贡献重算核验 ===")
    report = verify(events_a + events_b, publisher, downstream)
    print(f"  重算结果: {report.expected}")
    print(f"  发布方值: {report.publisher_value}, 下游值: {report.downstream_value}")
    if report.ok:
        print("  核验通过：乱序/撤回/环依赖处理、原子关联、幂等均一致 ✔")
        return 0
    for problem in report.problems:
        print(f"  问题: {problem}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
