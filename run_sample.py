"""Synthetic end-to-end sample: loan-application routing rules.

Run with:  python3.11 run_sample.py
"""

from model import EnumSet, FactSchema, IntRange, Rule
from analyzer import analyze, replay


def build_schema() -> FactSchema:
    return FactSchema(
        fact_id="loan-application",
        version=3,
        domains={
            "channel": EnumSet(["web", "branch", "partner"]),
            "tier": EnumSet(["gold", "silver", "bronze"]),
            "amount": IntRange(0, 1000),
            "tenure_months": IntRange(0, 360),
        },
    )


def build_rules() -> list[Rule]:
    gold = EnumSet(["gold"])
    web = EnumSet(["web"])
    return [
        # Same priority 100, overlapping on gold / amount 100..200,
        # different effects -> conflict.
        Rule("R-CONFLICT-A", 2, 100, "auto_approve",
             {"tier": gold, "amount": IntRange(0, 200)}),
        Rule("R-CONFLICT-B", 1, 100, "manual_review",
             {"tier": gold, "amount": IntRange(100, 300)}),
        # Fully covered by R-CONFLICT-A -> unreachable.
        Rule("R-SHADOWED", 1, 50, "notify_customer",
             {"tier": gold, "amount": IntRange(0, 150)}),
        # Partially overlaps R-STANDARD -> partial shadow only.
        Rule("R-FAST", 1, 80, "fast_track",
             {"channel": web, "amount": IntRange(50, 250)}),
        Rule("R-STANDARD", 1, 10, "standard_pipeline",
             {"channel": web, "amount": IntRange(100, 900)}),
        # Union coverage: R-SMALL is covered by the two lane rules together.
        Rule("R-UNION-WEB", 1, 90, "web_lane",
             {"channel": web, "amount": IntRange(0, 10)}),
        Rule("R-UNION-BRANCH", 1, 90, "branch_lane",
             {"channel": EnumSet(["branch"]), "amount": IntRange(0, 10)}),
        Rule("R-SMALL", 1, 40, "small_loan",
             {"channel": EnumSet(["web", "branch"]),
              "amount": IntRange(0, 10)}),
        # Wildcard catch-all, stays reachable.
        Rule("R-AUDIT", 1, 5, "audit_log", {}),
    ]


def print_report(rules, schema, max_operations: int) -> None:
    report = analyze(rules, schema, max_operations=max_operations)
    print(report.summary())
    print()
    print("coverage pairs (covering -> covered):")
    for covering, covered in report.coverage:
        print(f"  {covering}  covers  {covered}")
    print()
    print("findings:")
    for finding in report.findings:
        ok = replay(finding, rules, schema)
        verdict = "REPLAY OK" if ok else "REPLAY FAIL"
        print(f"  [{finding.finding_id}] v{finding.version} {finding.kind}"
              f"  ({verdict})")
        print(f"      rules: {', '.join(finding.rule_refs)}")
        print(f"      {finding.detail}")
        if finding.counterexample is not None:
            print(f"      counterexample: {dict(finding.counterexample)}")
    print()
    exhausted = [s for s in report.steps if s.kind == "budget_exhausted"]
    for step in exhausted:
        print(f"  {step.step_id} v{step.version} {step.kind}: {step.detail}")
    if exhausted:
        print()


def main() -> None:
    schema = build_schema()
    rules = build_rules()

    print("=== full run (sufficient budget) ===")
    print_report(rules, schema, max_operations=100_000)

    print("=== starved run (budget = 5) ===")
    print_report(rules, schema, max_operations=5)


if __name__ == "__main__":
    main()
