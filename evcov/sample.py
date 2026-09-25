"""Synthetic, replayable sample. Run: python3.11 -m evcov.sample

Scenario (all data synthetic):
  Rule `rule-structural-integrity` v3 requires, over horizon [0, 100):
    - category "telemetry": at least 2 independent sources
    - category "audit":     at least 1 independent source
  Evidence:
    ev-tel-1  src-alpha  telemetry  [0, 100)
    ev-tel-2  src-alpha  telemetry  [10, 70)   (same source as ev-tel-1: 1 source)
    ev-tel-3  src-beta   telemetry  [0, 100)
    ev-aud-1  src-gamma  audit      [0, 50)    (expires at t=50)
    ev-aud-2  src-delta  audit      [50, 100)  (takes over exactly at t=50)
"""

from __future__ import annotations

from .model import CategoryRequirement, EvidenceItem, Rule, Source
from .pipeline import replay, run_pipeline
from .sweep import compute_coverage


def build_synthetic() -> tuple[list[Source], Rule, list[EvidenceItem]]:
    sources = [
        Source("src-alpha", version=1),
        Source("src-beta", version=1),
        Source("src-gamma", version=1),
        Source("src-delta", version=1),
    ]
    rule = Rule(
        id="rule-structural-integrity",
        version=3,
        requirements=(
            CategoryRequirement("telemetry", min_sources=2),
            CategoryRequirement("audit", min_sources=1),
        ),
        horizon_start=0,
        horizon_end=100,
    )
    items = [
        EvidenceItem("ev-tel-1", "src-alpha", frozenset({"telemetry"}), 0, 100, version=1),
        EvidenceItem("ev-tel-2", "src-alpha", frozenset({"telemetry"}), 10, 70, version=2),
        EvidenceItem("ev-tel-3", "src-beta", frozenset({"telemetry"}), 0, 100, version=1),
        EvidenceItem("ev-aud-1", "src-gamma", frozenset({"audit"}), 0, 50, version=1),
        EvidenceItem("ev-aud-2", "src-delta", frozenset({"audit"}), 50, 100, version=1),
    ]
    return sources, rule, items


def _fmt(intervals) -> str:
    if not intervals:
        return "(none)"
    return ", ".join(f"[{iv.start}, {iv.end})" for iv in intervals)


def main() -> None:
    sources, rule, items = build_synthetic()

    print("=== 合成样例:证据覆盖计算 ===")
    print(f"规则 {rule.id} v{rule.version}: "
          + ", ".join(f"{r.category}>={r.min_sources} 独立来源" for r in rule.requirements)
          + f", 区间 [{rule.horizon_start}, {rule.horizon_end})")
    print(f"来源: {', '.join(f'{s.id}(v{s.version})' for s in sources)}")
    for it in items:
        print(f"  证据 {it.id} v{it.version}: 来源={it.source_id} "
              f"类别={sorted(it.categories)} 有效期=[{it.valid_from}, {it.valid_until})")

    report = run_pipeline(rule, items)
    cov = report.coverage
    print("\n--- 覆盖结果 ---")
    print(f"满足区间(并集后): {_fmt(cov.satisfied)}")
    print(f"缺口:             {_fmt(cov.gaps)}")
    print(f"最长满足区间:     [{cov.longest.start}, {cov.longest.end}) "
          f"长度={cov.longest.length}" if cov.longest else "最长满足区间: 无")

    sub = report.subset
    print("\n--- 最小证据子集 ---")
    print(f"可行: {sub.feasible}, 最优性已证明: {sub.optimality_proven}, "
          f"评估次数: {sub.evaluations}/{sub.budget}")
    print(f"见证子集({sub.witness_size} 条): {list(sub.witness)}")

    print("\n--- 可重放性 ---")
    print(f"重放校验: {'通过' if replay(report, rule, items) else '失败'}")
    for step in report.steps:
        print(f"  {step.step_id} v{step.version} ({step.kind}): "
              f"in={step.input_digest[:12]}... out={step.output_digest[:12]}...")

    print("\n--- 撤回最后支持者 ---")
    retracted = [it.retract() if it.id == "ev-tel-3" else it for it in items]
    cov2 = compute_coverage(rule, retracted)
    print("撤回 ev-tel-3 后(telemetry 仅剩 src-alpha 一个独立来源):")
    print(f"满足区间: {_fmt(cov2.satisfied)}")
    print(f"缺口:     {_fmt(cov2.gaps)}")


if __name__ == "__main__":
    main()
