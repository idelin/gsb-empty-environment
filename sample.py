"""Runnable synthetic-data demonstration."""

from fact_resolver import replay, resolve_facts


def evidence(evidence_id, version, source, reliability):
    return {
        "evidence_id": evidence_id,
        "version": version,
        "source": source,
        "reliability": reliability,
        "synthetic": True,
    }


def fact(fact_id, claim_id, value, reliability, valid_from, valid_until, source_id, source=None):
    return {
        "fact_id": fact_id,
        "version": "1.0.0",
        "claim_id": claim_id,
        "value": value,
        "evidence": evidence(source_id, "1.0.0", source or f"synthetic:{source_id}", reliability),
        "valid_from": valid_from,
        "valid_until": valid_until,
    }


FACTS = [
    fact("F-PRIORITY-LOW", "switch.armed", True, 1, "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "E-LAB"),
    fact("F-PRIORITY-HIGH", "switch.armed", False, 3, "2026-01-01T00:00:00Z", "2026-07-01T00:00:00Z", "E-CONSOLE"),
    fact("F-PRIORITY-MID", "switch.armed", True, 2, "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "E-AUDIT"),
    fact("F-DEADLOCK-T", "sensor.triggered", True, 2, "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "E-SENSOR-A"),
    fact("F-DEADLOCK-F", "sensor.triggered", False, 2, "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "E-SENSOR-B"),
    fact("F-DEADLOCK-LOW", "sensor.triggered", True, 1, "2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "E-MANUAL"),
]


def show(label, query_at, items):
    result = resolve_facts(items, query_at)
    rendered = result.canonical()
    print(f"\n=== {label} ({query_at}) ===")
    for claim in rendered["claims"]:
        print(
            f"claim {claim['claim_id']}: status={claim['status']} "
            f"value={claim['value']} winning_tier={claim['winning_tier']}"
        )
    for item in rendered["selected"]:
        print(f"  selected   {item['fact_id']}: {item['reason_code']}")
    for item in rendered["suppressed"]:
        print(f"  suppressed {item['fact_id']}: {item['reason_code']}")
    for item in rendered["inactive"]:
        print(f"  inactive   {item['fact_id']}: {item['reason_code']}")
    print(f"replay_digest={rendered['replay']['digest']}")

    operation = rendered["replay"]["canonical_operation"]
    replay_result = replay(operation)
    print(f"replay_matches={replay_result['matches']}")
    return result


def main():
    show("high tier active", "2026-03-01T00:00:00Z", FACTS)
    show("high tier expired; lower tier is effective again", "2026-09-01T00:00:00Z", FACTS)
    reversed_items = list(reversed(FACTS))
    first = resolve_facts(FACTS, "2026-03-01T00:00:00Z").canonical()
    reordered = resolve_facts(reversed_items, "2026-03-01T00:00:00Z").canonical()
    print("\norder_independent=", first["replay"]["digest"] == reordered["replay"]["digest"])


if __name__ == "__main__":
    main()
