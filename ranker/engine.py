"""主检索器：打分、过滤、剪枝，并为语料中每个文档产出解释节点。

评分公式（规则版本化，见 rules.py）：
    contribution(t, d) = query_weight(t) * tf(t, d) * idf(t)
    score(d)           = Σ_t contribution(t, d)      # 按 term 字典序求和
解释树中每个命中文档的分项合计严格等于最终分数。

剪枝（可靠得分上界）：
    查询词按 tub(t) = weight(t) * idf(t) * tf_max(t) 降序处理；
    处理到第 i 个词时，ub = 已处理分项之和 + Σ 剩余词的 tub。
    因为 tf(t, d) <= tf_max(t)，ub 是 d 最终分数的可靠上界。
    当堆已满（k 个精确分数）且 ub 不足以进入 top-k 时剪枝，
    记录 ub、当时阈值与分项证据；统计版本记录在根节点。

未命中不是笼统的“无结果”：每个未命中文档都有专属节点
（filtered / no_match / pruned / scored_out），可用 explain_miss 查询。
"""

from __future__ import annotations

import operator
from collections import Counter

from .explanation import child_key, finalize
from .rules import IDF_FUNCS, TOKENIZERS

_OPS = {
    "==": operator.eq, "!=": operator.ne,
    "<": operator.lt, "<=": operator.le,
    ">": operator.gt, ">=": operator.ge,
}


def eval_filter(flt: dict, meta: dict) -> bool:
    actual = meta.get(flt["field"])
    if flt["op"] == "in":
        return actual in flt["value"]
    if actual is None:
        return False
    return _OPS[flt["op"]](actual, flt["value"])


def filter_reason(flt: dict, meta: dict) -> str:
    actual = meta.get(flt["field"])
    return f"字段 {flt['field']}={actual!r} 不满足过滤条件 {flt['field']} {flt['op']} {flt['value']!r}"


def compute_stats(docs, snapshot: dict, tokenize) -> dict:
    """从语料快照独立复算统计：N、df、tf_max。验证器用它核对统计快照。"""
    df: Counter = Counter()
    tf_max: Counter = Counter()
    for doc_id, version in snapshot.items():
        rev = docs.get(doc_id, version)
        tf = Counter(tokenize(rev["text"]))
        for term, count in tf.items():
            df[term] += 1
            if count > tf_max[term]:
                tf_max[term] = count
    return {"N": len(snapshot), "df": dict(df), "tf_max": dict(tf_max)}


class Engine:
    def __init__(self, docs, stats, rules):
        self.docs = docs
        self.stats = stats
        self.rules = rules

    def build_stats(self) -> dict:
        """基于当前语料版本与当前规则的分词器，构建新的统计快照。"""
        rules = self.rules.latest()
        if rules is None:
            raise RuntimeError("缺少规则版本，无法构建统计")
        tokenize = TOKENIZERS[rules["tokenizer"]]
        snap = self.docs.snapshot()
        body = compute_stats(self.docs, snap, tokenize)
        body.update({
            "version": self.stats.next_version(),
            "corpus_version": self.docs.corpus_version,
            "rules_version": rules["version"],
        })
        self.stats.put(body)
        return body

    def search(self, phrases: list[dict], filters: list[dict] | None = None, k: int = 10) -> dict:
        """执行检索并返回完整解释树（含每个文档的账目节点）。

        phrases: [{"text": "机器学习", "weight": 1.0}, ...]；
        每个短语按同一分词规则切分，派生 token 继承短语权重，
        同一 token 重复出现时取最大权重。
        """
        rules = self.rules.latest()
        stats = self.stats.latest()
        if rules is None or stats is None:
            raise RuntimeError("缺少规则或统计快照，无法检索")
        if stats["corpus_version"] != self.docs.corpus_version:
            raise RuntimeError("统计快照已过期：语料变更后请先重建统计")
        tokenize = TOKENIZERS[rules["tokenizer"]]
        idf_func = IDF_FUNCS[rules["idf"]]
        filters = list(filters or [])

        weights: dict[str, float] = {}
        for phrase in phrases:
            for tok in tokenize(phrase["text"]):
                weights[tok] = max(weights.get(tok, 0.0), float(phrase.get("weight", 1.0)))
        terms = sorted(weights)
        idf = {t: idf_func(stats["N"], stats["df"].get(t, 0)) for t in terms}
        tub = {t: weights[t] * idf[t] * stats["tf_max"].get(t, 0) for t in terms}
        score_order = sorted(terms, key=lambda t: (-tub[t], t))

        snap = self.docs.snapshot()
        others: list[dict] = []   # filtered / no_match / pruned / scored_out
        exact: list[tuple] = []   # (score, doc_id, rev, contribs, tf)
        top: list[tuple] = []     # 当前已精确打分文档的 top-k (score, doc_id)

        for doc_id in sorted(snap):
            rev = self.docs.get(doc_id, snap[doc_id])
            base = {"doc_id": doc_id, "doc_version": rev["version"], "doc_hash": rev["hash"]}

            failed = next((f for f in filters if not eval_filter(f, rev["meta"])), None)
            if failed is not None:
                others.append({"type": "filtered", **base,
                               "filter": failed, "reason": filter_reason(failed, rev["meta"])})
                continue

            tf = Counter(tokenize(rev["text"]))
            absent = [t for t in terms if tf.get(t, 0) == 0]
            if len(absent) == len(terms):
                others.append({"type": "no_match", **base,
                               "query_terms": list(terms), "absent_terms": absent})
                continue

            contribs: dict[str, float] = {}
            processed: list[dict] = []
            partial = 0.0
            pruned_node = None
            for i, term in enumerate(score_order):
                if len(top) == k:
                    kth_score, kth_doc = top[-1]
                    ub = partial
                    remaining = []
                    for t2 in score_order[i:]:
                        bound = weights[t2] * idf[t2] * stats["tf_max"].get(t2, 0)
                        ub += bound
                        remaining.append({"term": t2, "weight": weights[t2], "idf": idf[t2],
                                          "tf_max": stats["tf_max"].get(t2, 0), "bound": bound})
                    if ub < kth_score or (ub == kth_score and doc_id > kth_doc):
                        pruned_node = {"type": "pruned", **base, "processed": processed,
                                       "remaining": remaining, "upper_bound": ub,
                                       "threshold": kth_score, "kth_doc_id": kth_doc}
                        break
                contribution = weights[term] * tf.get(term, 0) * idf[term]
                contribs[term] = contribution
                processed.append({"term": term, "weight": weights[term],
                                  "tf": tf.get(term, 0), "idf": idf[term],
                                  "contribution": contribution})
                partial += contribution
            if pruned_node is not None:
                others.append(pruned_node)
                continue

            score = 0.0
            for term in terms:
                score += contribs[term]
            exact.append((score, doc_id, rev, contribs, tf))
            top.append((score, doc_id))
            top.sort(key=lambda item: (-item[0], item[1]))
            del top[k:]

        exact.sort(key=lambda item: (-item[0], item[1]))
        final_top = exact[:k]
        kth_final = final_top[-1] if len(final_top) == k else None

        def term_nodes(contribs, tf):
            return [
                {"type": "term", "term": t, "weight": weights[t], "tf": tf[t],
                 "idf": idf[t], "contribution": contribs[t]}
                for t in terms if tf.get(t, 0) > 0
            ]

        results = []
        for rank, (score, doc_id, rev, contribs, tf) in enumerate(final_top, start=1):
            results.append({
                "type": "result", "rank": rank, "doc_id": doc_id,
                "doc_version": rev["version"], "doc_hash": rev["hash"],
                "score": score, "children": term_nodes(contribs, tf),
            })
        for score, doc_id, rev, contribs, tf in exact[k:]:
            others.append({
                "type": "scored_out", "doc_id": doc_id, "doc_version": rev["version"],
                "doc_hash": rev["hash"], "score": score, "children": term_nodes(contribs, tf),
                "threshold": kth_final[0], "kth_doc_id": kth_final[1],
            })

        root = {
            "type": "ranking",
            "rules_version": rules["version"],
            "stats_version": stats["version"],
            "corpus_version": self.docs.corpus_version,
            "query": {
                "phrases": [dict(p) for p in phrases],
                "terms": [{"term": t, "weight": weights[t]} for t in terms],
            },
            "filters": filters,
            "k": k,
            "children": sorted(results + others, key=child_key),
        }
        return finalize(root)


def explain_miss(explanation: dict, doc_id: str) -> dict:
    """查询指定文档的未命中原因（命中则返回命中信息）。绝不只给笼统的“无结果”。"""
    for child in explanation.get("children", []):
        if child.get("doc_id") != doc_id:
            continue
        ctype = child["type"]
        if ctype == "result":
            return {"doc_id": doc_id, "status": "hit",
                    "rank": child["rank"], "score": child["score"]}
        if ctype == "filtered":
            return {"doc_id": doc_id, "status": "filtered",
                    "reason": child["reason"], "filter": child["filter"]}
        if ctype == "no_match":
            return {"doc_id": doc_id, "status": "no_match",
                    "absent_terms": child["absent_terms"],
                    "detail": "文档不包含任何查询词：" + "、".join(child["absent_terms"])}
        if ctype == "pruned":
            return {"doc_id": doc_id, "status": "pruned",
                    "upper_bound": child["upper_bound"], "threshold": child["threshold"],
                    "kth_doc_id": child["kth_doc_id"],
                    "stats_version": explanation["stats_version"],
                    "detail": "可靠得分上界低于 top-k 阈值，被安全剪枝"}
        if ctype == "scored_out":
            return {"doc_id": doc_id, "status": "scored_out",
                    "score": child["score"], "threshold": child["threshold"],
                    "kth_doc_id": child["kth_doc_id"],
                    "detail": "精确得分低于 top-k 阈值"}
    return {"doc_id": doc_id, "status": "unknown_doc",
            "detail": "该文档不在本解释的语料账目中"}
