"""Synthetic demo: loan-approval knowledge base, counterfactual interventions,
support chains, and replayable evidence. Run: python3.11 run_sample.py"""
from counterfactual import (
    Engine,
    Fact,
    InterventionError,
    RemoveFact,
    ReplaceFact,
    Rule,
)

# --- synthetic baseline facts (stable IDs + versions) ----------------------
FACTS = (
    Fact("F-AGE", "age", "adult", 1),
    Fact("F-INCOME", "income", "high", 1),
    Fact("F-DEBT", "debt", "low", 1),
    Fact("F-REGION", "region", "supported", 1),
)

# --- synthetic rules (stable IDs + versions) -------------------------------
RULES = (
    Rule("R-CREDIT", (("age", "adult"), ("income", "high")), ("credit", "good"), 1),
    Rule("R-RISK", (("credit", "good"), ("debt", "low")), ("risk", "low"), 1),
    Rule("R-APPROVE", (("risk", "low"), ("region", "supported")), ("loan", "approved"), 1),
)

GOAL = ("loan", "approved")


def show_chain(title: str, derivation) -> None:
    print(f"\n== {title} ==")
    key, value = GOAL
    print(f"goal {key}={value}: {'HOLDS' if derivation.holds(key, value) else 'DOES NOT HOLD'}")
    if derivation.holds(key, value):
        chain = derivation.support_chain(key)
        for step in chain["steps"]:
            print(
                f"  step {step.step_id} (rule {step.rule_id} v{step.rule_version})"
                f" <- {list(step.input_fact_ids)}"
                f" => {step.output_fact.key}={step.output_fact.value}"
            )
        for fact in chain["source_facts"]:
            print(f"  source {fact.fact_id} v{fact.version}: {fact.key}={fact.value}")
    print(f"summary: {derivation.summary()}")
    print(f"evidence replayable: {derivation.replay(RULES)}")


def main() -> None:
    engine = Engine(FACTS, RULES)
    baseline_summary = engine.baseline_summary()
    show_chain("baseline", engine.baseline())

    # Intervention 1: temporarily remove the income fact.
    with engine.intervene(RemoveFact("F-INCOME")) as session:
        show_chain("intervention: remove F-INCOME", session.derivation)

    # Intervention 2: replace debt=low with debt=high (old value is dropped).
    with engine.intervene(ReplaceFact("F-DEBT", "high")) as session:
        show_chain("intervention: replace F-DEBT -> high", session.derivation)

    # Intervention 3: two isolated ops applied atomically.
    with engine.intervene(RemoveFact("F-REGION"), ReplaceFact("F-INCOME", "low")) as session:
        show_chain("intervention: remove F-REGION + replace F-INCOME -> low", session.derivation)

    # Contradictory interventions are rejected before anything is applied.
    for ops in (
        (ReplaceFact("F-DEBT", "high"), ReplaceFact("F-DEBT", "medium")),
        (RemoveFact("F-DEBT"), ReplaceFact("F-DEBT", "high")),
    ):
        try:
            with engine.intervene(*ops):
                pass
            print("ERROR: contradictory intervention was not rejected")
        except InterventionError as err:
            print(f"\nrejected contradictory intervention: {err}")

    print(f"\nbaseline summary before: {baseline_summary}")
    print(f"baseline summary after:  {engine.baseline_summary()}")
    print(f"baseline unchanged: {engine.baseline_summary() == baseline_summary}")


if __name__ == "__main__":
    main()
