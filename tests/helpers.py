"""测试共用的确定性语料：覆盖 result / filtered / no_match / pruned / scored_out。"""

from ranker import DocStore, Engine, RulesStore, StatsStore

QUERY = {
    "phrases": [{"text": "alpha", "weight": 2.0}, {"text": "beta", "weight": 1.0}],
    "filters": [{"field": "year", "op": ">=", "value": 2020}],
    "k": 3,
}


def build_world():
    docs = DocStore()
    docs.add("m1", "alpha alpha beta", {"year": 2022})
    docs.add("m2", "alpha beta", {"year": 2022})
    docs.add("m3", "alpha", {"year": 2022})
    docs.add("m4", "beta", {"year": 2022})          # 可靠上界低于阈值 -> pruned
    docs.add("m5", "gamma", {"year": 2022})         # 不含任何查询词 -> no_match
    docs.add("m6", "alpha beta", {"year": 2010})    # 违反过滤 -> filtered
    docs.add("m7", "alpha", {"year": 2022})         # 与 m3 同分但 doc_id 更大 -> scored_out
    rules = RulesStore()
    rules.add("unicode-unigram-v1", "idf-ln-v1")
    stats = StatsStore()
    engine = Engine(docs, stats, rules)
    engine.build_stats()
    return docs, stats, rules, engine


def child_of(expl, doc_id):
    for child in expl["children"]:
        if child.get("doc_id") == doc_id:
            return child
    raise KeyError(doc_id)
