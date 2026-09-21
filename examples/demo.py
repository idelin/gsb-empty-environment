"""乱序环绕计数器分析库 —— 端到端演示。

直接运行：python3 examples/demo.py
场景：
1. 250 -> 3 确定环绕（delta=9），与长缺测下的“不能解释成 9”对照
2. 相同数字：无 reset 为零增长；带 reset 为不可判定
3. 长时间缺测中间补样：确定总量守恒、不伪造
4. 迟到插入只修订相邻差分（作废/替换）
5. 撤回采样：相邻区间作废并合并重算
6. 同时间不同 seq 的决胜
所有结果都带采样 ID 与配置版本。
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from wrapcounter import (
    CounterAnalyzer,
    DuplicatePolicy,
    Reason,
    RevisionOp,
    Sample,
    SourceConfig,
    recompute_source,
)


def line(t: str) -> None:
    print(f"\n=== {t} ===")


def show_iv(iv) -> None:
    tag = "确定" if iv.status.value == "definite" else "不确定"
    delta = iv.delta if iv.delta is not None else "—"
    print(f"  [{iv.interval_id}] {iv.prev_value}->{iv.curr_value} "
          f"dt={iv.dt_seconds:>5.1f}s {tag} delta={delta} "
          f"reason={iv.reason.value} cfg=v{iv.config_version}")


def show_revs(revs) -> None:
    for r in revs:
        mark = "UPSERT" if r.op is RevisionOp.UPSERT else "REVOKE"
        replaced = f" replaced={r.replaced_interval_id}" if r.replaced_interval_id else ""
        print(f"  rev#{r.revision_id:<3} {mark:<6} {r.interval_id}{replaced}  # {r.reason}")


def main() -> None:
    cfg = SourceConfig(source="m", width_bits=8, max_gap_seconds=30, config_version=1)

    line("1. 250->3：间隔内不足一整圈 => 确定环绕 +9")
    samples = [Sample("a", "m", 250, 0), Sample("b", "m", 3, 10)]
    _, ivs, total = recompute_source(cfg, samples)
    show_iv(ivs[1])
    print(f"  确定总量={total.definite_delta} complete={total.complete}")

    line("1b. 同样 250->3 但间隔 40s > 30s => 不确定，不能当成 +9")
    _, ivs, total = recompute_source(
        cfg, [Sample("a", "m", 250, 0), Sample("b", "m", 3, 40)]
    )
    show_iv(ivs[1])
    print(f"  确定总量={total.definite_delta} complete={total.complete} "
          f"作废区间={total.uncertain_interval_ids}")

    line("2. 相同数字：无 reset=零增长；带 reset=不可判定")
    _, ivs, _ = recompute_source(cfg, [Sample("a", "m", 42, 0), Sample("b", "m", 42, 5)])
    show_iv(ivs[1])
    _, ivs, _ = recompute_source(
        cfg, [Sample("a", "m", 42, 0), Sample("b", "m", 42, 5, reset=True)]
    )
    show_iv(ivs[1])

    line("3/4. 乱序到达 + 迟到补样：只动相邻差分，总量守恒")
    engine = CounterAnalyzer()
    engine.register_source(cfg)
    print("  先到 a=250@0, c=9@30（粗区间确定 wrap +15）:")
    show_revs(engine.upsert_sample(Sample("a", "m", 250, 0, ingest_order=1)))
    show_revs(engine.upsert_sample(Sample("c", "m", 9, 30, ingest_order=2)))
    print(f"  总量={engine.source_total('m').definite_delta}")
    print("  迟到补样 b=4@15：旧 a->c 作废，拆为 a->b(+10) 与 b->c(+5):")
    show_revs(engine.upsert_sample(Sample("b", "m", 4, 15, ingest_order=3)))
    total = engine.source_total("m")
    print(f"  补样后确定总量={total.definite_delta} complete={total.complete} "
          f"样本链={total.ordered_sample_ids}")

    line("5. 撤回 b：a->b、b->c 作废，合并回 a->c")
    show_revs(engine.retract_sample("b"))
    print(f"  撤回后总量={engine.source_total('m').definite_delta}（守恒）")

    line("6. 同时间不同 seq：seq 小者在前（250@seq0 -> 3@seq1 仍是 +9）")
    ordered, ivs, _ = recompute_source(
        cfg,
        [Sample("hi", "m", 3, 7.0, seq=1), Sample("lo", "m", 250, 7.0, seq=0)],
    )
    print(f"  全序={[s.sample_id for s in ordered]}")
    show_iv(ivs[1])

    line("结果溯源：采样 ID + 配置版本")
    iv = engine.get_interval("m", "m:a->c")
    print(f"  {iv.interval_id} prev={iv.prev_sample_id} curr={iv.curr_sample_id} "
          f"cfg=v{iv.config_version}")


if __name__ == "__main__":
    main()
