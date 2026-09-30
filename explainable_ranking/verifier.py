"""Independent verifier for explanation trees.

The verifier never trusts the retriever's labels. It reloads the
historical evidence named by the tree (corpus snapshot, rules snapshot,
statistics snapshot), recomputes the whole ranking from scratch, and
compares the recomputation against the claimed tree node by node in
deterministic order. The first node that fails to recompute is
reported.

Verdicts:
  verified      - the tree recomputes exactly from the bound evidence.
  failed        - a node contradicts the evidence (tampering, omission,
                  swapped document, forged label, ...).
  unverifiable  - required historical evidence (rules / stats / corpus
                  snapshot / document) is missing, so nothing can be
                  checked. Missing statistics can ONLY yield this.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .corpus import CorpusStore
from .ranker import rank
from .rules import RuleStore
from .stats import StatsStore


@dataclass
class Verdict:
    status: str
    node_id: str | None = None
    field: str | None = None
    expected: object = None
    actual: object = None
    reason: str = ""

    def __str__(self) -> str:
        if self.status == "verified":
            return "VERIFIED: explanation tree recomputes exactly"
        if self.status == "unverifiable":
            return f"UNVERIFIABLE: {self.reason}"
        return (f"FAILED at node {self.node_id} field {self.field}: "
                f"{self.reason} (expected={self.expected!r}, "
                f"actual={self.actual!r})")


def _mismatch(expected: object, actual: object,
              node_id: str, field: str) -> Verdict | None:
    if isinstance(expected, dict) and isinstance(actual, dict):
        current = expected.get("node_id") or actual.get("node_id") or node_id
        if not isinstance(current, str):
            current = node_id
        missing = sorted(set(expected) - set(actual))
        extra = sorted(set(actual) - set(expected))
        if missing or extra:
            return Verdict("failed", node_id=current, field=field,
                           expected=missing, actual=extra,
                           reason="node keys differ "
                                  "(missing=expected-only, extra=actual-only)")
        for key in expected:
            verdict = _mismatch(expected[key], actual[key], current, key)
            if verdict is not None:
                return verdict
        return None
    if isinstance(expected, (list, tuple)) and isinstance(actual, (list, tuple)):
        if len(expected) != len(actual):
            return Verdict("failed", node_id=node_id, field=field,
                           expected=len(expected), actual=len(actual),
                           reason="entry count differs "
                                  "(omitted or injected nodes)")
        for index, (e_item, a_item) in enumerate(zip(expected, actual)):
            verdict = _mismatch(e_item, a_item, node_id, f"{field}[{index}]")
            if verdict is not None:
                return verdict
        return None
    if isinstance(expected, bool) or isinstance(actual, bool):
        if expected == actual:
            return None
        return Verdict("failed", node_id=node_id, field=field,
                       expected=expected, actual=actual,
                       reason="boolean mismatch")
    if isinstance(expected, (int, float)) and isinstance(actual, (int, float)):
        if math.isclose(expected, actual, rel_tol=1e-9, abs_tol=1e-12):
            return None
        return Verdict("failed", node_id=node_id, field=field,
                       expected=expected, actual=actual,
                       reason="numeric mismatch")
    if expected != actual:
        return Verdict("failed", node_id=node_id, field=field,
                       expected=expected, actual=actual,
                       reason="value mismatch")
    return None


def verify(tree: dict, corpus_store: CorpusStore,
           rule_store: RuleStore, stats_store: StatsStore) -> Verdict:
    if not isinstance(tree, dict) or tree.get("type") != "ranking":
        return Verdict("failed", node_id="root", field="type",
                       reason="not a ranking explanation tree")

    rules = rule_store.get(tree.get("rules_version"))
    if rules is None:
        return Verdict("unverifiable",
                       reason=f"rules version {tree.get('rules_version')!r} "
                              "is not available")
    stats = stats_store.get(tree.get("stats_version"))
    if stats is None:
        return Verdict("unverifiable",
                       reason=f"statistics snapshot "
                              f"{tree.get('stats_version')!r} is not available")
    corpus = corpus_store.get(tree.get("corpus_version"))
    if corpus is None:
        return Verdict("unverifiable",
                       reason=f"corpus snapshot "
                              f"{tree.get('corpus_version')!r} is not available")

    for child in tree.get("children", []):
        doc_id = child.get("doc_id")
        if doc_id not in corpus:
            return Verdict("unverifiable", node_id=child.get("node_id"),
                           reason=f"document {doc_id!r} is missing from "
                                  f"corpus snapshot "
                                  f"{tree.get('corpus_version')!r}")
        doc = corpus.get(doc_id)
        if child.get("doc_hash") != doc.content_hash:
            return Verdict("failed", node_id=child.get("node_id"),
                           field="doc_hash",
                           expected=child.get("doc_hash"),
                           actual=doc.content_hash,
                           reason="document content does not match the "
                                  "hash recorded in the explanation")
        if child.get("doc_version") != doc.version:
            return Verdict("failed", node_id=child.get("node_id"),
                           field="doc_version",
                           expected=child.get("doc_version"),
                           actual=doc.version,
                           reason="document version label does not match "
                                  "the explanation")

    query = tree.get("query", {}).get("raw")
    if not isinstance(query, str):
        return Verdict("failed", node_id="root", field="query",
                       reason="query text missing or not a string")

    expected = rank(query, corpus, tree["corpus_version"], rules, stats)
    verdict = _mismatch(expected, tree, "root", "node")
    if verdict is not None:
        return verdict
    return Verdict("verified")
