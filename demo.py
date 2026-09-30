"""End-to-end demo of the explainable, verifiable ranking pipeline.

Run with:  python3.11 demo.py
"""
from __future__ import annotations

import copy
import json

from explainable_ranking import (
    Corpus,
    CorpusStore,
    Document,
    Filter,
    RuleSet,
    RuleStore,
    StatsSnapshot,
    StatsStore,
    explain_miss,
    rank,
    tokenize,
    verify,
)

QUERY = "检索 引擎 ranking"


def build_world():
    docs_v1 = [
        Document("d1", "检索引擎入门 ranking ranking search engine 教程", "v1"),
        Document("d2", "搜索引擎检索 ranking ranking ranking 实战", "v1"),
        Document("d3", "spam 检索引擎 ranking", "v1"),
        Document("d4", "短文", "v1"),
        Document("d5", "阳光明媚心情愉悦", "v1"),
        Document("d6", "检索引擎深入原理分析 ranking ranking 系统设计实现", "v1"),
        Document("d7", "检索系统优化笔记 ranking ranking 引擎实践", "v1"),
        Document("d8", "检索一下而已随便看看东西", "v1"),
    ]
    corpus_v1 = Corpus()
    for doc in docs_v1:
        corpus_v1.put(doc)
    corpus_store = CorpusStore()
    corpus_store.put("corpus-v1", corpus_v1)
    stats_store = StatsStore()
    stats_store.put(StatsSnapshot.build(
        "stats-v1", [tokenize(corpus_v1.get(d).text) for d in corpus_v1.ids()]))
    rule_store = RuleStore()
    rule_store.put(RuleSet(
        version="rules-v1",
        weights={"检": 1.0, "索": 1.0, "引": 1.0, "擎": 1.0, "ranking": 1.5},
        filters=(Filter("min_tokens", 3), Filter("forbidden_term", "spam")),
        top_k=3,
        score_threshold=8.0,
    ))
    return corpus_store, rule_store, stats_store


def show_hits(tree):
    for child in tree["children"]:
        if child["type"] == "hit":
            print(f"  #{child['rank']} {child['doc_id']} "
                  f"score={child['score']:.6f} "
                  f"(sum of {len(child['components'])} term components, "
                  f"stats={child['stats_version']})")


def main():
    corpus_store, rule_store, stats_store = build_world()
    corpus_v1 = corpus_store.get("corpus-v1")
    rules_v1 = rule_store.get("rules-v1")
    stats_v1 = stats_store.get("stats-v1")

    print("=" * 72)
    print("1. Ranking with explanation tree")
    print("=" * 72)
    tree = rank(QUERY, corpus_v1, "corpus-v1", rules_v1, stats_v1)
    print(f"query={QUERY!r} -> tokens={tree['query']['tokens']}")
    show_hits(tree)
    print("\nexplanation tree (JSON, abridged to root + first two children):")
    abridged = dict(tree, children=tree["children"][:2] + ["..."])
    print(json.dumps(abridged, ensure_ascii=False, indent=2))

    print("\n" + "=" * 72)
    print("2. Miss explanations (never a generic 'no results')")
    print("=" * 72)
    for doc_id in ("d3", "d4", "d5", "d7", "d8", "d99"):
        print(f"  {doc_id}: "
              + json.dumps(explain_miss(tree, doc_id), ensure_ascii=False))

    print("\n" + "=" * 72)
    print("3. Independent verification of the clean tree")
    print("=" * 72)
    print(" ", verify(tree, corpus_store, rule_store, stats_store))

    print("\n" + "=" * 72)
    print("4. Rule/stats/corpus updates: old explanations stay valid")
    print("=" * 72)
    corpus_v2 = Corpus()
    for doc_id in corpus_v1.ids():
        corpus_v2.put(corpus_v1.get(doc_id))
    corpus_v2.put(Document("d9", "新的文档 ranking 检索引擎技术展望", "v1"))
    corpus_store.put("corpus-v2", corpus_v2)
    stats_store.put(StatsSnapshot.build(
        "stats-v2", [tokenize(corpus_v2.get(d).text) for d in corpus_v2.ids()]))
    rule_store.put(RuleSet(
        version="rules-v2",
        weights={"检": 1.0, "索": 1.0, "引": 1.0, "擎": 1.0, "ranking": 2.0},
        filters=(Filter("min_tokens", 3), Filter("forbidden_term", "spam")),
        top_k=5,
        score_threshold=8.0,
    ))
    print("  published corpus-v2 / stats-v2 / rules-v2 "
          "(new doc d9, ranking weight 1.5 -> 2.0, top_k 3 -> 5)")
    print("  old tree (bound to v1):",
          verify(tree, corpus_store, rule_store, stats_store))
    tree_v2 = rank(QUERY, corpus_v2, "corpus-v2",
                   rule_store.get("rules-v2"), stats_store.get("stats-v2"))
    print("  new tree (bound to v2):",
          verify(tree_v2, corpus_store, rule_store, stats_store))
    print(f"  old tree pins stats_version={tree['stats_version']!r}, "
          f"new tree pins stats_version={tree_v2['stats_version']!r}")

    print("\n" + "=" * 72)
    print("5. Adversarial scenarios: locate the first failing node")
    print("=" * 72)

    tampered = copy.deepcopy(tree)
    target = None
    for child in tampered["children"]:
        if child["type"] == "hit":
            child["components"][0]["weight"] += 0.5
            target = child["node_id"]
            break
    print(f"  a) injected tampered weight into {target}:")
    print("    ", verify(tampered, corpus_store, rule_store, stats_store))

    tampered = copy.deepcopy(tree)
    tampered["children"] = [c for c in tampered["children"]
                            if not (c["type"] == "filtered"
                                    and c["doc_id"] == "d3")]
    print("  b) omitted the filtered node for d3:")
    print("    ", verify(tampered, corpus_store, rule_store, stats_store))

    evil_corpus_store = copy.deepcopy(corpus_store)
    evil_corpus = evil_corpus_store.get("corpus-v1")
    evil_corpus.put(Document("d1", "被篡改的文档内容 completely replaced", "v1"))
    print("  c) replaced document d1 inside corpus snapshot corpus-v1:")
    print("    ", verify(tree, evil_corpus_store, rule_store, stats_store))

    tampered = copy.deepcopy(tree)
    for child in tampered["children"]:
        if child.get("reason") == "beyond_top_k":
            child["type"] = "hit"
            child["rank"] = 1
            break
    print("  d) forged retriever label (pruned -> hit):")
    print("    ", verify(tampered, corpus_store, rule_store, stats_store))

    broken_stats = copy.deepcopy(stats_store)
    broken_stats.drop("stats-v1")
    print("  e) statistics snapshot stats-v1 no longer available:")
    print("    ", verify(tree, corpus_store, rule_store, broken_stats))


if __name__ == "__main__":
    main()
