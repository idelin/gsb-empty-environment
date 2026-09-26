#!/usr/bin/env python3.11
"""端到端运行样例（Python 3.11，仅标准库）。

用法：
    python3.11 demo.py

内容：
1. 加载本地合成 UTF-8 语料 data/corpus.txt（不使用任何搜索服务）；
2. 构建显式单词同义组/对与短语同义组（两类分离）；
3. 展示带权查询图：原词、扩展词、证据链、跳数、回边、共享节点；
4. 用小词图简单路径枚举核验 a↔b 循环与权重；
5. 对若干查询执行 查询图 × 位置倒排 的联合 TopK，
   证据中区分 原词/扩展词/原短语/扩展短语，短语保留路径长度；
6. 用极小预算展示截断：结果头部明确标注“未完成”。
"""

from __future__ import annotations

import pathlib

from syno import PositionalIndex, QueryExpander, Searcher, SynonymStore
from syno.query_graph import enumerate_simple_paths, _build_adj

ROOT = pathlib.Path(__file__).resolve().parent
CORPUS = ROOT / "data" / "corpus.txt"

QUERIES = [
    "fast car",
    "vehicle",
    "kicked the bucket",
    "new york",
    "机器学习",
]


def load_corpus(index: PositionalIndex, path: pathlib.Path) -> None:
    """解析 data/corpus.txt：空行分文档，"ID: xxx" 给编号，# 为注释。"""
    doc_id = None
    lines: list[str] = []

    def flush() -> None:
        nonlocal doc_id, lines
        if doc_id is not None:
            text = "\n".join(lines).strip()
            if text:
                index.add_document(doc_id, text)
        doc_id, lines = None, []

    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.rstrip()
        if not line.strip():
            flush()
            continue
        if line.startswith("#"):
            continue
        if line.startswith("ID:"):
            flush()
            doc_id = line.split(":", 1)[1].strip()
            continue
        lines.append(line)
    flush()


def build_store() -> SynonymStore:
    st = SynonymStore()
    st.add_word_group(["car", "automobile", "vehicle"], 0.7)
    st.add_word_pair("fast", "quick", 0.6)
    st.add_word_pair("truck", "vehicle", 0.5)
    # a↔b：双向，且重复声明同一对只保留最大权重边
    st.add_word_group(["a", "b"], 0.8)
    st.add_phrase_group(["kicked the bucket", "died"], 0.8)
    st.add_phrase_group(["new york", "nyc"], 0.9)
    st.add_phrase_group(["机器学习", "人工智能"], 0.75)
    return st


def print_rule(title: str) -> None:
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def show_graph(graph) -> None:
    print(f"查询: {graph.text!r}")
    print(f"token: {list(graph.tokens)}  最大跳数={graph.max_word_hops} "
          f"节点预算={graph.node_budget} 边预算={graph.edge_budget}")
    print("-- 单词层（带权词图节点）--")
    for surface, info in sorted(graph.nodes.items(), key=lambda kv: (kv[1].hop, kv[0])):
        tag = "原词" if info.is_original else "扩展"
        roots = ",".join(str(r) for r in info.roots)
        per_root = ", ".join(
            f"根{r}:w={w:.3g}/{info.paths_by_root[r]}"
            for r, w in sorted(info.weights_by_root.items())
        )
        share = " [共享]" if surface in graph.shared else ""
        print(f"  [{tag}] {surface:<12} hop={info.hop} 根=[{roots}]{share}")
        print(f"        归因: {per_root}")
    print("-- 短语层（整条替换，不拆散）--")
    if not graph.phrases:
        print("  （无）")
    for pa in graph.phrases:
        span = " ".join(graph.tokens[pa.start:pa.end])
        print(f"  查询位置 [{pa.start},{pa.end}) 原短语={span!r} 组边权={pa.group_weight}")
        for toks, surf, is_orig in zip(pa.alternatives, pa.surfaces, pa.is_original):
            tag = "原短语" if is_orig else "扩展短语"
            print(f"    [{tag}] {' '.join(toks)!r} 长度={len(toks)} 显示={surf!r}")
    print(f"回边计数(循环只展开一次的证据)={graph.back_edges}  共享节点={sorted(graph.shared)}")
    if graph.truncated:
        print(f"!! 扩展未完成：预算截断 truncation={graph.truncation}")


def show_result(result) -> None:
    if result.truncated:
        print(f"[未完成] 扩展在预算 {result.truncation} 处被截断，TopK 仅覆盖部分扩展")
    if not result.results:
        print("  （无命中）")
        return
    for rank, dr in enumerate(result.results, 1):
        print(f"  #{rank} {dr.doc_id}  score={dr.score:.4f}")
        for e in dr.evidences:
            snippet_tag = e.source_tag()
            print(
                f"      - {snippet_tag:<4} 概念={e.concept_label!r} "
                f"命中={e.surface!r} w={e.weight:<5} "
                f"位置={e.start} 跨度={e.length} 证据链=[{e.source_chain}]"
            )


def main() -> None:
    print_rule("分词规则（确定性，无外部库）")
    print("  word: [A-Za-z0-9]+ 连续串；CJK/假名按单字切分；")
    print("  空白与标点丢弃；casefold 小写；保留 token 序号与字符偏移。")

    index = PositionalIndex()
    load_corpus(index, CORPUS)
    print_rule(f"本地语料：{index.doc_count()} 篇文档（{CORPUS}）")
    for did, doc in index.docs.items():
        head = doc.text.replace("\n", " ")
        print(f"  {did}: {len(doc.tokens):>2} tokens | {head[:60]}...")

    store = build_store()
    expander = QueryExpander(store, max_word_hops=2, node_budget=128, edge_budget=512)

    print_rule("小词图核验：a↔b 循环简单路径枚举（只展开一次）")
    demo_graph = expander.expand("a")
    adj = _build_adj(demo_graph.nodes, store)
    paths = enumerate_simple_paths(adj, "a", "b", max_hops=2)
    print(f"  闭包节点={sorted(adj)}  回边={demo_graph.back_edges}")
    for path, w in paths:
        print(f"    路径 {' -> '.join(path)}  权重={w:.3g}")
    print("  不存在 a->b->a->b 之类无限/重复展开（path-guard + 单跳数上限）")

    for q in QUERIES:
        print_rule(f"查询图：{q!r}")
        graph = expander.expand(q)
        show_graph(graph)
        print("-- 联合 TopK（查询图 × 位置倒排，位置归属不重复计分）--")
        searcher = Searcher(index, expander, top_k=3)
        show_result(searcher.search(q))

    print_rule("预算截断演示：node_budget=2，明确标记未完成")
    tiny_expander = QueryExpander(store, max_word_hops=2, node_budget=2, edge_budget=512)
    graph = tiny_expander.expand("fast car")
    show_graph(graph)
    print("-- TopK --")
    show_result(Searcher(index, tiny_expander, top_k=3).search("fast car"))
    print()
    print("说明：截断后只对已入图的 surface 建候选，故结果可能少召回，")
    print("      调用方看到 [未完成] 后可增大预算或缩小查询重试。")


if __name__ == "__main__":
    main()
