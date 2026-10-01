"""独立验证器：不信任主检索器的任何标签，仅凭证据复算。

证据 = 文档库历史版本 + 指定版本统计快照 + 规则版本。
判定：
- valid         全部节点按规范顺序复算通过；
- invalid       报告首个不成立节点（规范顺序下路径最小者）及理由；
- unverifiable  缺少统计/规则/文档历史等证据 —— 只能不可验证，不能判无效。

复算内容：
- 统计快照本身：用语料快照独立重算 N/df/tf_max 并逐项比对；
- 命中：tf/idf/权重/分项逐项复算，分项合计必须等于最终分数；
- 过滤：在文档元数据上重新求值，理由文本重新生成比对；
- 剪枝：用 tf_max 重算上界，并核对上界确实低于最终 top-k 阈值；
- 账目：解释必须覆盖语料快照中的每个文档恰好一次。
"""

from __future__ import annotations

from collections import Counter

from .engine import compute_stats, eval_filter, filter_reason
from .explanation import child_key, node_hash
from .rules import IDF_FUNCS, TOKENIZERS
from .store import content_hash


class _Invalid(Exception):
    def __init__(self, path: str, reason: str):
        super().__init__(reason)
        self.path = path
        self.reason = reason


class _Unverifiable(Exception):
    pass


class _Ctx:
    def __init__(self, tokenize, weights, idf, tf_max, score_order, terms, filters):
        self.tokenize = tokenize
        self.weights = weights
        self.idf = idf
        self.tf_max = tf_max
        self.score_order = score_order
        self.terms = terms
        self.filters = filters


def _child_path(node: dict) -> str:
    if node["type"] == "result":
        return f"root/result#{node['rank']}({node['doc_id']})"
    return f"root/{node['type']}({node['doc_id']})"


class Verifier:
    def __init__(self, docs, stats, rules):
        self.docs = docs
        self.stats = stats
        self.rules = rules

    def verify(self, expl: dict) -> dict:
        try:
            self._verify(expl)
        except _Unverifiable as exc:
            return {"verdict": "unverifiable", "first_failure": None, "reason": str(exc)}
        except _Invalid as exc:
            return {"verdict": "invalid",
                    "first_failure": {"path": exc.path, "reason": exc.reason}}
        return {"verdict": "valid", "first_failure": None}

    # ------------------------------------------------------------------

    def _verify(self, expl: dict) -> None:
        # 1) 证据可用性：缺失只能判不可验证
        rules = self.rules.get(expl.get("rules_version"))
        if rules is None:
            raise _Unverifiable(f"规则版本 {expl.get('rules_version')} 缺失，无法复算")
        tokenize = TOKENIZERS.get(rules["tokenizer"])
        idf_func = IDF_FUNCS.get(rules["idf"])
        if tokenize is None or idf_func is None:
            raise _Unverifiable(f"未知规则实现: {rules}")
        stats = self.stats.get(expl.get("stats_version"))
        if stats is None:
            raise _Unverifiable(
                f"统计快照版本 {expl.get('stats_version')} 缺失：缺少统计证据，只能判不可验证")
        snap = self.docs.snapshot(expl.get("corpus_version"))
        if snap is None:
            raise _Unverifiable(f"语料版本 {expl.get('corpus_version')} 缺失")
        for doc_id, version in snap.items():
            if self.docs.get(doc_id, version) is None:
                raise _Unverifiable(f"文档 {doc_id} 的历史版本 {version} 缺失")

        # 2) 统计证据自洽：用语料快照独立重算并比对
        recomputed = compute_stats(self.docs, snap, tokenize)
        if (recomputed["N"] != stats["N"] or recomputed["df"] != stats["df"]
                or recomputed["tf_max"] != stats["tf_max"]):
            raise _Invalid("root", "统计快照与语料快照的独立复算结果不一致")
        if stats.get("corpus_version") != expl.get("corpus_version"):
            raise _Invalid("root", "统计快照与解释绑定的语料版本不一致")

        # 3) 哈希链完整性（防惰性篡改；语义复算才是最终依据）
        if expl.get("hash") != node_hash(expl):
            raise _Invalid("root", "解释树哈希链不一致（内容被篡改且未重算哈希）")

        # 4) 根节点结构：排名序号、顺序、文档账目完整
        children = expl.get("children", [])
        k = expl["k"]
        results = sorted((c for c in children if c["type"] == "result"),
                         key=lambda c: c["rank"])
        if [c["rank"] for c in results] != list(range(1, len(results) + 1)):
            raise _Invalid("root", "排名序号不连续")
        if len(results) > k:
            raise _Invalid("root", f"命中数 {len(results)} 超过 k={k}")
        for prev, nxt in zip(results, results[1:]):
            ok = prev["score"] > nxt["score"] or (
                prev["score"] == nxt["score"] and prev["doc_id"] < nxt["doc_id"])
            if not ok:
                raise _Invalid("root", "结果顺序与 (分数降序, doc_id 升序) 规则不符")
        accounted = [c["doc_id"] for c in children]
        if len(set(accounted)) != len(accounted):
            raise _Invalid("root", "文档账目中存在重复")
        missing = sorted(set(snap) - set(accounted))
        extra = sorted(set(accounted) - set(snap))
        if missing or extra:
            raise _Invalid("root", f"文档账目不完整：缺少 {missing}，多余 {extra}")

        # 5) 逐节点复算（规范顺序，首个不成立即返回）
        weights = {e["term"]: e["weight"] for e in expl["query"]["terms"]}
        terms = sorted(weights)
        idf = {t: idf_func(stats["N"], stats["df"].get(t, 0)) for t in terms}
        tf_max = {t: stats["tf_max"].get(t, 0) for t in terms}
        tub = {t: weights[t] * idf[t] * tf_max[t] for t in terms}
        ctx = _Ctx(tokenize, weights, idf, tf_max,
                   sorted(terms, key=lambda t: (-tub[t], t)), terms,
                   expl.get("filters", []))
        final_kth = None
        if len(results) == k:
            final_kth = (results[-1]["score"], results[-1]["doc_id"])
        for child in sorted(children, key=child_key):
            self._verify_child(child, ctx, final_kth)

    # ------------------------------------------------------------------

    def _doc_revision(self, node: dict, path: str) -> dict:
        rev = self.docs.get(node["doc_id"], node["doc_version"])
        if rev is None:
            raise _Unverifiable(
                f"文档 {node['doc_id']} 版本 {node['doc_version']} 缺失")
        # 不信任库中存储的哈希字段，直接从文本重算
        if content_hash(rev["text"]) != node["doc_hash"] or rev["hash"] != node["doc_hash"]:
            raise _Invalid(path, "文档哈希不匹配：文档版本被替换或篡改")
        return rev

    def _verify_child(self, child: dict, ctx: _Ctx, final_kth) -> None:
        path = _child_path(child)
        rev = self._doc_revision(child, path)
        ctype = child["type"]

        if ctype == "filtered":
            failed = next((f for f in ctx.filters if not eval_filter(f, rev["meta"])), None)
            if failed is None:
                raise _Invalid(path, "过滤理由不成立：该文档实际通过所有过滤条件")
            if failed != child["filter"]:
                raise _Invalid(path, "记录的过滤条件与首个不成立的过滤条件不符")
            if child["reason"] != filter_reason(failed, rev["meta"]):
                raise _Invalid(path, "过滤理由文本与复算结果不符")
            return

        tf = Counter(ctx.tokenize(rev["text"]))

        if ctype == "no_match":
            absent = [t for t in ctx.terms if tf.get(t, 0) == 0]
            if (child["query_terms"] != ctx.terms or child["absent_terms"] != absent
                    or len(absent) != len(ctx.terms)):
                raise _Invalid(path, "未命中理由不成立：复算发现文档包含查询词或记录不符")
            return

        if ctype in ("result", "scored_out"):
            self._check_terms_and_score(child, path, tf, ctx)
            if ctype == "scored_out":
                if final_kth is None:
                    raise _Invalid(path, "top-k 未满却存在落选精确评分")
                kth_score, kth_doc = final_kth
                if child["threshold"] != kth_score or child["kth_doc_id"] != kth_doc:
                    raise _Invalid(path, "落选阈值记录与最终 top-k 不符")
                if not (child["score"] < kth_score
                        or (child["score"] == kth_score and child["doc_id"] > kth_doc)):
                    raise _Invalid(path, "该文档实际应进入 top-k")
            return

        if ctype == "pruned":
            self._check_pruned(child, path, tf, ctx, final_kth)
            return

        raise _Invalid(path, f"未知节点类型 {ctype!r}")

    def _check_terms_and_score(self, node: dict, path: str, tf, ctx: _Ctx) -> None:
        expected_terms = sorted(t for t in ctx.terms if tf.get(t, 0) > 0)
        child_terms = [c["term"] for c in node.get("children", [])]
        if child_terms != expected_terms:
            raise _Invalid(path, f"分项列表不完整或被篡改：应为 {expected_terms}，实为 {child_terms}")
        contribs = {}
        for term_node in node.get("children", []):
            t = term_node["term"]
            cpath = f"{path}/term({t})"
            if term_node["weight"] != ctx.weights[t]:
                raise _Invalid(cpath, f"权重 {term_node['weight']} 与查询记录 "
                                      f"{ctx.weights[t]} 不一致（疑似篡改）")
            if term_node["tf"] != tf.get(t, 0):
                raise _Invalid(cpath, f"tf 复算不符：记录 {term_node['tf']}，"
                                      f"复算 {tf.get(t, 0)}")
            if term_node["idf"] != ctx.idf[t]:
                raise _Invalid(cpath, f"idf 复算不符：记录 {term_node['idf']}，"
                                      f"复算 {ctx.idf[t]}")
            expected = term_node["weight"] * term_node["tf"] * term_node["idf"]
            if term_node["contribution"] != expected:
                raise _Invalid(cpath, f"分项复算不符：记录 {term_node['contribution']}，"
                                      f"复算 {expected}")
            contribs[t] = term_node["contribution"]
        score = 0.0
        for t in expected_terms:
            score += contribs[t]
        if node["score"] != score:
            raise _Invalid(path, f"分项合计 {score} 与最终分数 {node['score']} 不符")

    def _check_pruned(self, node: dict, path: str, tf, ctx: _Ctx, final_kth) -> None:
        processed = node["processed"]
        remaining = node["remaining"]
        if ([p["term"] for p in processed] != ctx.score_order[:len(processed)]
                or [r["term"] for r in remaining] != ctx.score_order[len(processed):]):
            raise _Invalid(path, "剪枝分项的处理顺序与规则不符")
        ub = 0.0
        for p in processed:
            t = p["term"]
            if (p["weight"] != ctx.weights[t] or p["tf"] != tf.get(t, 0)
                    or p["idf"] != ctx.idf[t]):
                raise _Invalid(f"{path}/processed({t})", "已处理分项的证据复算不符")
            expected = p["weight"] * p["tf"] * p["idf"]
            if p["contribution"] != expected:
                raise _Invalid(f"{path}/processed({t})",
                               f"分项复算不符：记录 {p['contribution']}，复算 {expected}")
            ub += expected
        for r in remaining:
            t = r["term"]
            if (r["weight"] != ctx.weights[t] or r["idf"] != ctx.idf[t]
                    or r["tf_max"] != ctx.tf_max[t]):
                raise _Invalid(f"{path}/remaining({t})", "上界分项的统计证据复算不符")
            bound = r["weight"] * r["idf"] * r["tf_max"]
            if r["bound"] != bound:
                raise _Invalid(f"{path}/remaining({t})",
                               f"上界复算不符：记录 {r['bound']}，复算 {bound}")
            ub += bound
        if node["upper_bound"] != ub:
            raise _Invalid(path, f"上界复算不符：记录 {node['upper_bound']}，复算 {ub}")
        if final_kth is None:
            raise _Invalid(path, "top-k 未满却发生剪枝")
        kth_score, kth_doc = final_kth
        if not (ub < kth_score or (ub == kth_score and node["doc_id"] > kth_doc)):
            raise _Invalid(path, "剪枝上界不足以排除该文档（剪枝不可靠）")
