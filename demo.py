"""End-to-end demo of the window result combiner.

Scenario (as specified):

1. Window W1: A reports v1=9 then v2=5 (v1 is stale), B first reports under
   another rule version (window stays pending), then a compatible B v1=3.
   -> publish 8, bound to version vector {A:2, B:1}.
2. A corrects itself after the publish (v3=7) -> compensating event (+2),
   the historical record is left untouched.
3. Window W2: A is split into A1/A2 via a handover manifest; the old A and
   the new sub-shards arrive interleaved; the old A is rejected and never
   double counted.
4. The process crashes between the publish-record write and the downstream
   ack. After restart, recovery re-delivers the same result id and the
   downstream dedupes it (delta idempotency).
5. Window W3 times out with a missing shard -> partial publish.
   Window W4 has a slice revoked after publish -> compensating event.
6. The independent validator re-checks everything from the files on disk.
"""
from __future__ import annotations

import shutil
import sys
from pathlib import Path

from combiner.combiner import SimulatedCrash, WindowCombiner
from combiner.downstream import DownstreamSink
from combiner.protocol import REVOKE, HandoverManifest, ShardReport
from combiner.storage import Storage
from combiner.validator import validate


def show(title: str, events: list[dict]) -> None:
    for event in events:
        kind = event["type"]
        if kind == "published":
            record = event["record"]
            detail = (f"{record['kind']} id={record['result_id']} "
                      f"vector={record['version_vector']} "
                      f"value={record['value']} delta={record['delta']} "
                      f"complete={record['complete']}")
            print(f"    [{title}] published: {detail}")
        elif kind in ("report_rejected", "slice_revoked", "window_timeout",
                      "manifest_applied"):
            print(f"    [{title}] {kind}: {event}")


def main() -> int:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else "data")
    shutil.rmtree(root, ignore_errors=True)
    sink = DownstreamSink()
    combiner = WindowCombiner(Storage(root), sink)

    print("== W1: 兼容切片汇合 ==")
    combiner.open_window("W1", expected={"A", "B"}, deadline=100)
    show("B/r2", combiner.ingest(ShardReport(
        "W1", "B", epoch=1, local_version=1, watermark=10,
        rule_version="r2", value=3)))
    assert combiner.windows["W1"].status == "open", "规则版本不兼容时必须保持未决"
    show("A v1=9", combiner.ingest(ShardReport(
        "W1", "A", 1, 1, 10, "r1", 9)))
    show("A v2=5", combiner.ingest(ShardReport(
        "W1", "A", 1, 2, 10, "r1", 5)))
    show("A v1 旧版本重传", combiner.ingest(ShardReport(
        "W1", "A", 1, 1, 10, "r1", 9)))
    show("B/r1 v1=3", combiner.ingest(ShardReport(
        "W1", "B", 1, 1, 10, "r1", 3)))
    w1 = combiner.windows["W1"]
    assert w1.effective_value == 8, f"期望 8，实际 {w1.effective_value}"
    w1_first_id = w1.last_result_id
    print(f"  -> W1 发布 8 (A v2=5 + B v1=3)，结果ID {w1_first_id}")

    print("== W1: 发布后更正 -> 补偿事件 ==")
    show("A v3=7", combiner.ingest(ShardReport(
        "W1", "A", 1, 3, 10, "r1", 7)))
    history = Storage(root).read_published()
    assert history[w1_first_id]["value"] == 8, "历史结果不允许被改写"
    assert w1.effective_value == 10
    print(f"  -> 补偿 +2（supersedes {w1_first_id}），历史记录仍为 8")

    print("== W2: 交接清单 + 交错抵达 + 崩溃恢复 ==")
    combiner.open_window("W2", expected={"A", "B"}, deadline=100)
    show("旧A v1=100", combiner.ingest(ShardReport(
        "W2", "A", 1, 1, 20, "r1", 100)))
    show("manifest A->A1,A2", combiner.apply_manifest(
        HandoverManifest("W2", "A", ("A1", "A2"), epoch=2)))
    show("A1 v1=4", combiner.ingest(ShardReport(
        "W2", "A1", 2, 1, 20, "r1", 4)))
    show("旧A v2=9 (迟到)", combiner.ingest(ShardReport(
        "W2", "A", 1, 2, 20, "r1", 9)))
    show("A2 v1=6", combiner.ingest(ShardReport(
        "W2", "A2", 2, 1, 20, "r1", 6)))
    ids_before = set(Storage(root).read_published())
    combiner.fail_before_ack = True  # 在发布记录写入、下游确认之间崩溃
    try:
        combiner.ingest(ShardReport("W2", "B", 1, 1, 20, "r1", 3))
        raise AssertionError("应当注入崩溃")
    except SimulatedCrash as exc:
        print(f"  !! 进程崩溃: {exc}")
    new_ids = set(Storage(root).read_published()) - ids_before
    assert len(new_ids) == 1
    crashed_id = new_ids.pop()
    del combiner  # 模拟进程退出，内存状态全部丢失

    restarted = WindowCombiner(Storage(root), sink)
    reemitted = restarted.recover()
    assert len(reemitted) == 1, f"恢复时应恰好补发 1 条，实际 {len(reemitted)}"
    recovered_id = reemitted[0]["result_id"]
    assert recovered_id == crashed_id, "恢复后的结果ID必须与崩溃前一致"
    assert reemitted[0]["version_vector"] == {"A1": 1, "A2": 1, "B": 1}
    assert reemitted[0]["value"] == 13, "旧A(100/9)不得计入"
    dupes = sink.apply_count - len(sink.records)
    assert dupes == 1, "崩溃前已投递过一次，恢复重发必须被下游幂等去重"
    print(f"  -> 恢复补发同一结果ID {recovered_id}，下游去重 {dupes} 次，"
          f"W2=13 (A1+A2+B)")

    print("== W3: 缺片超时 ==")
    restarted.open_window("W3", expected={"C", "D"}, deadline=50)
    restarted.ingest(ShardReport("W3", "C", 1, 1, 30, "r1", 5))
    show("tick(60)", restarted.tick(60))
    w3_record = restarted.storage.read_published()[
        restarted.windows["W3"].last_result_id]
    assert w3_record["complete"] is False and w3_record["missing"] == ["D"]
    print("  -> 超时部分发布 value=5, missing=['D']")

    print("== W4: 发布后撤销 -> 补偿事件 ==")
    restarted.open_window("W4", expected={"E", "F"}, deadline=100)
    restarted.ingest(ShardReport("W4", "E", 1, 1, 40, "r1", 1))
    restarted.ingest(ShardReport("W4", "F", 1, 1, 40, "r1", 2))
    show("revoke F", restarted.ingest(ShardReport(
        "W4", "F", 1, 2, 40, "r1", kind=REVOKE)))
    assert restarted.windows["W4"].effective_value == 1
    print("  -> 撤销 F 后补偿 -2，有效值回到 1")

    print("== 独立校验器 ==")
    problems = validate(root)
    assert not problems, f"校验失败: {problems}"
    print("  -> 日志完整、发布记录无部分写入、无重复确认、快照与重放一致")
    print(f"  -> 下游合计 {sink.total}（8 +2 +13 +5 +3 -2 = 29），"
          f"应用 {len(sink.records)} 条 / 投递 {sink.apply_count} 次")
    assert sink.total == 29
    print("全部断言通过。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
