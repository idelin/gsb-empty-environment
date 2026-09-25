"""Synthetic sample knowledge base (grant-eligibility domain).

Demonstrates: alternative proofs (private vs public evidence path for the
means test), shared subproofs across multiple root goals, versioned ids,
and irrelevant evidence that must never enter the export.
"""
from __future__ import annotations

from .model import Fact, KnowledgeBase, Rule

GOALS = ("f-eligible", "f-means-tested")


def build_sample_kb() -> KnowledgeBase:
    facts = [
        Fact("f-age", 1, "Applicant age record: 34", public=False),
        Fact("f-income", 2, "Full income statement (sensitive)", public=False),
        Fact("f-income-redacted", 2, "Redacted public income summary", public=True),
        Fact("f-employment", 1, "Employment verification letter", public=False),
        Fact("f-identity", 3, "Government ID registry hit", public=True),
        Fact("f-adult", 1, "Applicant is an adult", base=False),
        Fact("f-means-tested", 1, "Applicant passes the means test", base=False),
        Fact("f-eligible", 4, "Applicant is eligible for the grant", base=False),
        # Irrelevant evidence: must never enter the export package.
        Fact("f-lottery-win", 1, "Unrelated: applicant won a lottery", public=True),
        Fact("f-vip", 1, "Unrelated: VIP status", base=False),
    ]
    rules = [
        Rule("r-adult", 2, ("f-age",), "f-adult"),
        # Alternative proof 1: private path, no extra public disclosure.
        Rule("r-means-private", 1, ("f-income", "f-employment"), "f-means-tested"),
        # Alternative proof 2: smaller, but leaks a public fact.
        Rule("r-means-public", 1, ("f-income-redacted",), "f-means-tested"),
        Rule("r-eligible", 3, ("f-adult", "f-means-tested", "f-identity"), "f-eligible"),
        Rule("r-noise", 1, ("f-lottery-win",), "f-vip"),
    ]
    return KnowledgeBase(facts, rules)
