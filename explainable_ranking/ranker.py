"""Ranking pipeline that emits a self-describing explanation tree.

Scoring rules (stable contract; the independent verifier recomputes
them from raw evidence):

  contribution(t, d) = weight(t) * idf(t) * (1 + ln(tf(t, d)))
  score(d)           = fsum of contribution(t, d) over matched query terms
  upper_bound(d)     = fsum over ALL query terms of
                       weight(t) * idf(t) * (1 + ln(tf_max(d)))

tf_max(d) is the maximum term frequency inside d. Because
tf(t, d) <= tf_max(d) for every term and all weights are non-negative,
upper_bound(d) >= score(d) always holds, so pruning a candidate whose
upper bound is below the threshold is safe and reproducible.

Every document of the corpus snapshot appears exactly once in the
tree, as one of: hit / filtered / pruned. Node order is deterministic
(documents sorted by doc_id).
"""
from __future__ import annotations

import itertools
import math
from collections import Counter

from .corpus import Corpus
from .rules import RuleSet
from .stats import StatsSnapshot
from .tokenizer import tokenize


def _term_contribution(rules: RuleSet, stats: StatsSnapshot,
                       term: str, tf: int) -> dict:
    weight = rules.weight(term)
    idf = stats.idf(term)
    return {
        "term": term,
        "tf": tf,
        "weight": weight,
        "idf": idf,
        "contribution": weight * idf * (1.0 + math.log(tf)),
    }


def rank(query: str, corpus: Corpus, corpus_version: str,
         rules: RuleSet, stats: StatsSnapshot) -> dict:
    qtokens = tokenize(query)
    qterms = list(dict.fromkeys(qtokens))
    ids = itertools.count(1)
    children: list[dict] = []
    scored: list[dict] = []

    for doc_id in corpus.ids():
        doc = corpus.get(doc_id)
        assert doc is not None
        tokens = tokenize(doc.text)
        node: dict = {
            "node_id": f"n{next(ids):04d}",
            "doc_id": doc_id,
            "doc_version": doc.version,
            "doc_hash": doc.content_hash,
        }
        filt = rules.first_matching_filter(tokens)
        if filt is not None:
            node["type"] = "filtered"
            node["filter"] = {"kind": filt.kind, "value": filt.value}
            node["reason"] = filt.describe(tokens)
            children.append(node)
            continue

        tf = Counter(tokens)
        components = [_term_contribution(rules, stats, t, tf[t])
                      for t in qterms if tf.get(t)]
        score = math.fsum(c["contribution"] for c in components)
        tf_max = max(tf.values()) if tf else 0
        upper_bound = (
            math.fsum(rules.weight(t) * stats.idf(t) * (1.0 + math.log(tf_max))
                      for t in qterms)
            if tf_max else 0.0
        )
        node["type"] = "scored"
        node["components"] = components
        node["score"] = score
        node["sum_components"] = math.fsum(c["contribution"]
                                           for c in components)
        node["score_equals_component_sum"] = math.isclose(
            score, node["sum_components"], rel_tol=0.0, abs_tol=1e-12)
        node["upper_bound"] = upper_bound
        node["stats_version"] = stats.version
        scored.append(node)
        children.append(node)

    survivors: list[dict] = []
    for node in scored:
        if node["upper_bound"] < rules.score_threshold:
            node["type"] = "pruned"
            node["reason"] = "upper_bound_below_threshold"
            node["detail"] = (f"upper_bound={node['upper_bound']!r} < "
                              f"score_threshold={rules.score_threshold!r}")
        elif node["score"] < rules.score_threshold:
            node["type"] = "pruned"
            node["reason"] = "score_below_threshold"
            node["detail"] = (f"score={node['score']!r} < "
                              f"score_threshold={rules.score_threshold!r}")
        else:
            survivors.append(node)

    survivors.sort(key=lambda n: (-n["score"], n["doc_id"]))
    for position, node in enumerate(survivors):
        if position < rules.top_k:
            node["type"] = "hit"
            node["rank"] = position + 1
        else:
            node["type"] = "pruned"
            node["reason"] = "beyond_top_k"
            node["detail"] = (f"rank_position={position + 1} > "
                              f"top_k={rules.top_k}")

    return {
        "node_id": "root",
        "type": "ranking",
        "query": {"raw": query, "tokens": qtokens, "terms": qterms},
        "corpus_version": corpus_version,
        "rules_version": rules.version,
        "stats_version": stats.version,
        "score_threshold": rules.score_threshold,
        "top_k": rules.top_k,
        "children": children,
    }


def explain_miss(tree: dict, doc_id: str) -> dict:
    """Explain why ``doc_id`` is absent from the hit list.

    Never returns a generic "no results": the answer always names the
    concrete stage (filtered / pruned / not in corpus) plus the data
    needed to recompute it.
    """
    for child in tree.get("children", []):
        if child.get("doc_id") != doc_id:
            continue
        ctype = child.get("type")
        if ctype == "hit":
            return {"doc_id": doc_id, "missed": False,
                    "rank": child.get("rank"), "score": child.get("score")}
        if ctype == "filtered":
            return {"doc_id": doc_id, "missed": True, "category": "filtered",
                    "filter": child["filter"], "reason": child["reason"]}
        if ctype == "pruned":
            return {"doc_id": doc_id, "missed": True, "category": "pruned",
                    "reason": child["reason"], "detail": child["detail"],
                    "score": child.get("score"),
                    "upper_bound": child.get("upper_bound"),
                    "stats_version": child.get("stats_version")}
        return {"doc_id": doc_id, "missed": True, "category": "unknown",
                "reason": f"unexpected node type {ctype!r}"}
    return {"doc_id": doc_id, "missed": True, "category": "not_in_corpus",
            "reason": (f"document is not part of corpus snapshot "
                       f"{tree.get('corpus_version')!r}")}
