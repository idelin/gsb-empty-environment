"""查询图 × 位置倒排的联合 TopK 检索与评分。

核心机制：
1. 为每个查询根生成概念候选：
   - 单词概念：根位置上的所有 surface（原词 + 同义扩展词），
     权重来自查询图 weights_by_root（路径边权乘积）。
   - 短语概念：查询中注册短语的整条替换（短语不拆散），
     原短语权重 1.0，扩展短语权重 = 短语组边权。
2. 在位置倒排中取回每个候选的精确出现位置。
3. 全局位置归属：把所有候选证据按
   原词/原短语优先 -> 短语优先 -> 权重高 -> 路径短 -> 概念 -> 位置
   排序后贪心占用文档 token 位置；每个 token 位置只归属一个概念的一个
   surface，因此：
     - 原词与扩展词在同一位置同时命中不会叠分；
     - 共享扩展（多个查询根经不同路径到达同一 surface）不重复计分。
4. 概念得分采用带饱和与封顶的对数项频，原词与扩展词合并计入同一项频，
   原词 bonus 固定不随命中数增长，故混合命中得分有上界，不无界叠加。
   文档得分为各概念分之和（共享扩展在同一概念内只产生一条归属）。
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

from .index import PositionalIndex
from .query_graph import QueryExpander, QueryGraph

SAT_K1 = 1.2
ORIGINAL_BONUS = 0.5


@dataclass
class Evidence:
    concept: str               # 概念 id，如 "w:2" / "p:0-2"
    concept_label: str         # 人类可读概念，如 "car" / "fast car"
    surface: str               # 文档中实际命中的文本
    kind: str                  # "word" / "phrase"
    start: int                 # 起始 token 位置
    length: int                # 跨度（token 数；短语保留其路径长度）
    weight: float              # 扩展传播权重
    is_original: bool
    source_chain: str          # 原词 -> ... -> surface 的证据链
    char_start: int
    char_end: int

    def source_tag(self) -> str:
        if self.is_original:
            return "原词" if self.kind == "word" else "原短语"
        return "扩展词" if self.kind == "word" else "扩展短语"


@dataclass
class DocResult:
    doc_id: str
    score: float
    evidences: list[Evidence] = field(default_factory=list)


@dataclass
class TopKResult:
    query: str
    graph: QueryGraph
    results: list[DocResult]
    truncated: bool
    truncation: str | None


class Searcher:
    def __init__(
        self,
        index: PositionalIndex,
        expander: QueryExpander,
        top_k: int = 5,
        k1: float = SAT_K1,
        original_bonus: float = ORIGINAL_BONUS,
    ) -> None:
        self.index = index
        self.expander = expander
        self.top_k = top_k
        self.k1 = k1
        self.original_bonus = original_bonus

    def search(self, query: str) -> TopKResult:
        graph = self.expander.expand(query)

        concepts: dict[str, dict] = {}
        self._build_word_concepts(graph, concepts)
        self._build_phrase_concepts(graph, concepts)

        cand_docs: set[str] = set()
        for c in concepts.values():
            for cand in c["cands"]:
                cand_docs.update(cand["positions"].keys())

        scored: list[DocResult] = []
        for doc_id in sorted(cand_docs):
            result = self._score_document(doc_id, concepts)
            if result is not None:
                scored.append(result)

        scored.sort(key=lambda r: (-r.score, r.doc_id))
        return TopKResult(
            query=query,
            graph=graph,
            results=scored[: self.top_k],
            truncated=graph.truncated,
            truncation=graph.truncation,
        )

    # ------------------------------------------------------------------
    # 概念构建
    # ------------------------------------------------------------------
    def _build_word_concepts(self, graph: QueryGraph, concepts: dict) -> None:
        for pos, root in enumerate(graph.tokens):
            surfaces = [
                (
                    surface,
                    info.weights_by_root[pos],
                    info.paths_by_root[pos],
                    info.is_original,
                )
                for surface, info in graph.nodes.items()
                if pos in info.roots
            ]
            if not surfaces:
                continue
            cands = []
            vocab: set[str] = set()
            for surface, weight, path, is_orig in surfaces:
                vocab.add(surface)
                positions = {
                    did: poses
                    for did in self.index.postings.get(surface, {})
                    if (poses := self.index.term_positions(did, surface))
                }
                cands.append({
                    "surface": surface,
                    "kind": "word",
                    "span_len": 1,
                    "weight": weight,
                    "path": path,
                    "is_original": is_orig,
                    "positions": positions,
                })
            concepts[f"w:{pos}"] = {
                "label": root,
                "kind": "word",
                "idf": self._word_idf(vocab),
                "cands": cands,
            }

    def _build_phrase_concepts(self, graph: QueryGraph, concepts: dict) -> None:
        for pa in graph.phrases:
            cid = f"p:{pa.start}-{pa.end}"
            label = " ".join(graph.tokens[pa.start:pa.end])
            cands = []
            vocab: set[str] = set()
            for alt_tokens, alt_surface, is_orig in zip(
                pa.alternatives, pa.surfaces, pa.is_original
            ):
                weight = 1.0 if is_orig else pa.group_weight
                positions = {
                    did: poses
                    for did in self.index.docs
                    if (poses := self.index.phrase_positions(did, alt_tokens))
                }
                cands.append({
                    "surface": alt_surface,
                    "kind": "phrase",
                    "span_len": len(alt_tokens),
                    "weight": weight,
                    "path": (alt_surface,) if is_orig else (label, alt_surface),
                    "is_original": is_orig,
                    "positions": positions,
                })
                vocab.update(alt_tokens)
            idf = sum(self._word_idf({w}) for w in vocab) / len(vocab)
            concepts[cid] = {
                "label": label,
                "kind": "phrase",
                "idf": idf,
                "cands": cands,
            }

    # ------------------------------------------------------------------
    # 打分与位置归属
    # ------------------------------------------------------------------
    def _score_document(self, doc_id: str, concepts: dict[str, dict]):
        raw: list[dict] = []
        for cid, c in concepts.items():
            for cand in c["cands"]:
                for start in cand["positions"].get(doc_id, ()):
                    raw.append({
                        "cid": cid, "c": c, "cand": cand,
                        "start": start, "length": cand["span_len"],
                    })
        if not raw:
            return None

        # 全局位置归属：同一 token 位置只允许一条证据占用。
        raw.sort(key=_evidence_priority)
        occupied: set[int] = set()
        chosen: list[dict] = []
        for item in raw:
            span = set(range(item["start"], item["start"] + item["length"]))
            if span & occupied:
                continue
            occupied |= span
            chosen.append(item)

        by_concept: dict[str, list[dict]] = {}
        for item in chosen:
            by_concept.setdefault(item["cid"], []).append(item)

        evidences: list[Evidence] = []
        score = 0.0
        for cid, items in by_concept.items():
            c = items[0]["c"]
            n_orig = sum(1 for it in items if it["cand"]["is_original"])
            n_exp = len(items) - n_orig
            best_w = max(it["cand"]["weight"] for it in items)
            # 饱和项频：原词与扩展词合并计入同一 tf；
            # 原词 bonus 固定、不随命中数增长 -> 原词/扩展同中不叠分。
            tf_merged = n_orig + best_w * n_exp
            sat = tf_merged / (self.k1 + tf_merged)
            concept_score = c["idf"] * sat
            if n_orig:
                concept_score += self.original_bonus * best_w
            score += concept_score
            for it in items:
                cand = it["cand"]
                _, cs, ce = self.index.text_at(doc_id, it["start"], it["length"])
                evidences.append(Evidence(
                    concept=cid,
                    concept_label=c["label"],
                    surface=cand["surface"],
                    kind=cand["kind"],
                    start=it["start"],
                    length=it["length"],
                    weight=round(cand["weight"], 4),
                    is_original=cand["is_original"],
                    source_chain=" -> ".join(cand["path"]),
                    char_start=cs,
                    char_end=ce,
                ))

        evidences.sort(key=lambda e: (e.start, e.concept))
        return DocResult(doc_id=doc_id, score=score, evidences=evidences)

    def _word_idf(self, vocab: set[str]) -> float:
        n = self.index.doc_count()
        dfs = [self.index.doc_frequency(w) for w in vocab] or [0]
        df = max(dfs)  # 概念稀有度取最强代表词，保证为正
        return math.log(1.0 + (n + 1.0) / (df + 1.0))


def _evidence_priority(item):
    cand = item["cand"]
    layer = 0 if cand["is_original"] else 1
    kind_order = 0 if cand["kind"] == "phrase" else 1
    return (
        layer,
        kind_order,
        -round(cand["weight"], 12),
        len(cand["path"]),
        item["cid"],
        item["start"],
    )
