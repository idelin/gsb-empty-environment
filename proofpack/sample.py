"""Synthetic end-to-end demo (all data is fictitious).

Scenario: a loan decision.  ``F_ELIGIBLE`` and ``F_PREMIUM_OK`` are the
root goals.  Eligibility has two alternative proofs: a cheap one that
leaks a public guarantor declaration, and a more expensive all-private
one.  The optimiser must prefer fewer leaked public facts first, then
the smaller package.  ``F_CREDIT_OK`` is shared by both goals and must
be derived (and counted) only once.

Run:  python3.11 -m proofpack.sample
"""

from __future__ import annotations

import json

from .export import export_package
from .model import Fact, KnowledgeBase, Rule
from .search import solve
from .verify import minimality_failures, verify_package

GOALS = ["F_ELIGIBLE", "F_PREMIUM_OK"]


def build_kb(reverse: bool = False) -> KnowledgeBase:
    facts = [
        Fact("F_KYC", 1, "KYC check passed (synthetic)", public=False),
        Fact("F_ACCT_6M", 1, "account older than 6 months", public=False),
        Fact("F_PAYSLIP", 2, "recent payslip, v2 format", public=False),
        Fact("F_BANK_ATTEST", 1, "bank attestation letter", public=False),
        Fact("F_TAX_RETURN", 1, "published tax return", public=True),
        Fact("F_CREDIT_REPORT", 1, "purchased credit report", public=True),
        Fact("F_GUARANTOR", 1, "public guarantor declaration", public=True),
        Fact("F_CREDIT_OK", 1, "creditworthiness established", evidence=False),
        Fact("F_INCOME_OK", 1, "income established", evidence=False),
        Fact("F_ELIGIBLE", 1, "loan eligibility", evidence=False),
        Fact("F_PREMIUM_OK", 1, "premium tier qualification", evidence=False),
    ]
    rules = [
        Rule("R_ELIG_STD", 1, ("F_CREDIT_OK", "F_INCOME_OK"), "F_ELIGIBLE"),
        Rule("R_ELIG_GUAR", 1, ("F_GUARANTOR",), "F_ELIGIBLE"),
        Rule("R_CREDIT_REPORT", 1, ("F_CREDIT_REPORT",), "F_CREDIT_OK"),
        Rule("R_CREDIT_ATTEST", 2, ("F_BANK_ATTEST",), "F_CREDIT_OK"),
        Rule("R_ATTEST", 1, ("F_KYC", "F_ACCT_6M"), "F_BANK_ATTEST"),
        Rule("R_INCOME_PAYSLIP", 1, ("F_PAYSLIP",), "F_INCOME_OK"),
        Rule("R_INCOME_TAX", 1, ("F_TAX_RETURN",), "F_INCOME_OK"),
        Rule("R_PREMIUM", 1, ("F_CREDIT_OK",), "F_PREMIUM_OK"),
    ]
    if reverse:
        facts.reverse()
        rules.reverse()
    return KnowledgeBase(facts, rules)


def main() -> None:
    kb = build_kb()
    print("=== synthetic loan-decision knowledge base ===")
    print(f"facts: {len(kb.facts)}  rules: {len(kb.rules)}  goals: {GOALS}")

    result = export_package(kb, GOALS)
    search = result.search
    print("\n=== exact search ===")
    print(f"status: {search.status}  expansions: {search.expansions}")
    print(f"cost (leaked public facts, package items): {search.lower}")
    plan = search.plan
    print(f"base facts: {sorted(plan.base_facts)}")
    print(f"rules:      {sorted(plan.rules)}")

    pkg = result.package
    print("\n=== export package (canonical JSON, pretty-printed) ===")
    print(json.dumps(pkg, indent=2, sort_keys=True, ensure_ascii=False))
    print(f"\nstable byte length: {len(result.data)}")
    print(f"content sha256:     {pkg['summary']['sha256']}")

    print("\n=== determinism check ===")
    again = export_package(build_kb(reverse=True), GOALS)
    print(f"identical bytes from reordered KB: {again.data == result.data}")

    print("\n=== offline verification ===")
    report = verify_package(json.loads(result.data))
    print(json.dumps(report, indent=2, sort_keys=True))

    print("\n=== minimality: removing any single item must fail ===")
    failures = minimality_failures(pkg)
    print(f"items whose removal still verifies: {failures or 'none'}")

    print("\n=== budget-exhausted search returns unresolved bounds ===")
    bounded = solve(kb, GOALS, budget=0)
    print(f"status: {bounded.status}")
    print(f"lower bound: {bounded.lower}  upper bound: {bounded.upper}")


if __name__ == "__main__":
    main()
