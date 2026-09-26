"""qexpand -- 基于显式同义组的查询扩展 + 带权词图 + 位置倒排 TopK 检索。

仅使用 Python 3.11 标准库；不使用任何搜索服务或全文检索库。

分词规则（tokenize）
-------------------
1. 全文 casefold（大小写归一，兼容 Unicode）；
2. ASCII 字母 / 数字 / 下划线的最长连续串为一个词元，如 ``foo_bar``、``v2``；
3. 其余任何 Unicode 单词字符（中日韩汉字、带重音字母等）逐字成词元，
   例如 "机器学习" -> ["机", "器", "学", "习"]（标准库无分词器，故按字切分）；
4. 空白与标点仅作分隔符，不产生词元。

扩展规则
--------
* 单词同义组与短语同义组分开存储、分开生效，互不混用；
* 只对查询**原词**做一跳扩展：a<->b 循环只展开一次，扩展出的 b 不会再
  反向或传递扩展（a->b->c 不会发生），从机制上杜绝循环扩展与指数组合；
* 多词同义以带权词图表示：原词位置为图节点，同义短语为带权边 / 边链，
  路径枚举保留路径长度与原词位置（original_span）；
* 短语成员必须在原词流中**连续**出现才触发扩展，多词同义不可拆散匹配；
* 路径枚举有预算上限 max_paths，超出即截断并将 truncated 置真，
  调用方可明确得知结果不完整。

计分规则
--------
* 原词边权重 1.0，扩展边权重 expansion_weight（默认 0.8），原词恒高于扩展；
* 路径权重为各边权重之积（短语边链只首边携带扩展权重，其余为 1.0）；
* 同一文档同一匹配区间 (start, end) 只取最高权重计一次：
  共享扩展（同一扩展词来自多个同义组 / 多条路径）不重复计分；
* 同一文档内不同区间的命中以概率和 1 - Π(1 - w) 合并，总分恒 <= 1：
  原词与扩展词同时命中不会无界叠分；
* 对查询图全部（未截断的）路径与位置倒排联合计分后，用堆取 TopK。
"""

from __future__ import annotations

import heapq
import re
from collections import defaultdict
from dataclasses import dataclass

ORIGINAL_WEIGHT = 1.0
DEFAULT_EXPANSION_WEIGHT = 0.8
DEFAULT_MAX_PATHS = 64

_TOKEN_RE = re.compile(r"[a-z0-9_]+|[^\W\s]", re.UNICODE)


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的分词规则把 UTF-8 文本切成词元序列。"""
    return _TOKEN_RE.findall(text.casefold())


# ---------------------------------------------------------------- 同义组

class SynonymGroups:
    """显式同义组：单词组与短语组分开存放。"""

    def __init__(self) -> None:
        self.word_groups: list[frozenset[str]] = []
        self.phrase_groups: list[frozenset[tuple[str, ...]]] = []
        self._word_to_groups: dict[str, list[int]] = defaultdict(list)

    def add_word_group(self, words) -> int:
        """登记一个单词同义组（成员必须都是单词元），返回组号。"""
        group = frozenset(words)
        if len(group) < 2:
            raise ValueError("同义组至少需要两个不同成员")
        if any(len(tokenize(w)) != 1 for w in group):
            raise ValueError("单词同义组只接受单词元成员，多词请用 add_phrase_group")
        gid = len(self.word_groups)
        self.word_groups.append(group)
        for w in group:
            self._word_to_groups[w].append(gid)
        return gid

    def add_phrase_group(self, phrases) -> int:
        """登记一个短语同义组，成员为字符串或词元元组，至少一个多词成员。"""
        members = []
        for p in phrases:
            t = tuple(tokenize(p)) if isinstance(p, str) else tuple(p)
            if not t:
                raise ValueError("短语成员不能为空")
            members.append(t)
        group = frozenset(members)
        if len(group) < 2:
            raise ValueError("同义组至少需要两个不同成员")
        if not any(len(t) > 1 for t in group):
            raise ValueError("短语同义组至少需要一个多词成员，单词请用 add_word_group")
        gid = len(self.phrase_groups)
        self.phrase_groups.append(group)
        return gid

    def word_synonyms(self, token: str) -> list[tuple[str, int]]:
        """返回 token 的单词同义 [(同义词, 组号)]；只查单词组，不涉短语组。"""
        out = []
        for gid in self._word_to_groups.get(token, ()):
            for w in self.word_groups[gid]:
                if w != token:
                    out.append((w, gid))
        return out


# ---------------------------------------------------------------- 词图

@dataclass(frozen=True)
class Edge:
    """带权边。src/dst 为原词位置节点（短语边链的中间节点为内部节点）。

    group_id 为 ("w", 组号) 或 ("p", 组号)，原词边为 None。
    """

    src: int
    dst: int
    term: str
    weight: float
    original: bool
    group_id: tuple | None


class WordGraph:
    """查询词图：节点 0..n 为原词位置，边为原词或同义扩展。"""

    def __init__(self, num_tokens: int) -> None:
        self.num_tokens = num_tokens
        self.start = 0
        self.end = num_tokens
        self.adj: dict[int, list[Edge]] = defaultdict(list)
        self._seen: set[tuple] = set()
        self._next_node = num_tokens + 1

    def _new_node(self) -> int:
        node = self._next_node
        self._next_node += 1
        return node

    def add_edge(self, edge: Edge) -> bool:
        """加边；同 (src, dst, term) 的边去重（共享扩展只进图一次）。"""
        key = (edge.src, edge.dst, edge.term)
        if key in self._seen:
            return False
        self._seen.add(key)
        self.adj[edge.src].append(edge)
        return True


def build_query_graph(
    query_tokens: list[str],
    groups: SynonymGroups,
    expansion_weight: float = DEFAULT_EXPANSION_WEIGHT,
) -> WordGraph:
    """把查询词元建成带权词图。只对原词做一跳扩展，扩展词不再扩展。"""
    n = len(query_tokens)
    graph = WordGraph(n)
    for i, tok in enumerate(query_tokens):
        graph.add_edge(Edge(i, i + 1, tok, ORIGINAL_WEIGHT, True, None))
    # 单词同义：一跳，权重 < 1
    for i, tok in enumerate(query_tokens):
        for syn, gid in groups.word_synonyms(tok):
            graph.add_edge(Edge(i, i + 1, syn, expansion_weight, False, ("w", gid)))
    # 短语同义：成员必须在原词流中连续出现，不可拆散
    for gid, group in enumerate(groups.phrase_groups):
        for member in group:
            m = len(member)
            for start in range(n - m + 1):
                if tuple(query_tokens[start:start + m]) != member:
                    continue
                end = start + m
                for alt in group:
                    if alt != member:
                        _add_phrase_alternative(graph, start, end, alt,
                                                expansion_weight, ("p", gid))
    return graph


def _add_phrase_alternative(graph: WordGraph, start: int, end: int,
                            alt_tokens: tuple[str, ...], weight: float,
                            group_key: tuple) -> None:
    """把同义短语加成 start->end 的边链；首边携带扩展权重，续边权重 1.0。"""
    node = start
    for j, term in enumerate(alt_tokens):
        last = j == len(alt_tokens) - 1
        nxt = end if last else graph._new_node()
        graph.add_edge(Edge(node, nxt, term, weight if j == 0 else ORIGINAL_WEIGHT,
                            False, group_key))
        node = nxt


@dataclass(frozen=True)
class Path:
    """一条从图起点到终点的路径：一个具体的查询短语解释。"""

    edges: tuple[Edge, ...]

    @property
    def terms(self) -> tuple[str, ...]:
        return tuple(e.term for e in self.edges)

    @property
    def weight(self) -> float:
        w = 1.0
        for e in self.edges:
            w *= e.weight
        return w

    @property
    def is_original(self) -> bool:
        return all(e.original for e in self.edges)

    @property
    def length(self) -> int:
        """路径长度（边数 = 词元数）。"""
        return len(self.edges)

    @property
    def original_span(self) -> tuple[int, int]:
        """该路径覆盖的原词位置区间 [src, dst)。"""
        return (self.edges[0].src, self.edges[-1].dst)

    @property
    def group_keys(self) -> tuple:
        return tuple(sorted({e.group_id for e in self.edges
                             if e.group_id is not None}))


def enumerate_paths(graph: WordGraph,
                    max_paths: int = DEFAULT_MAX_PATHS) -> tuple[list[Path], bool]:
    """枚举起点到终点的全部路径，最多 max_paths 条。

    返回 (paths, truncated)；truncated 为真表示预算耗尽、枚举不完整。
    图由构造保证是 DAG（边只向前），同义循环（a<->b）因一跳扩展不会成环。
    """
    if max_paths < 1:
        raise ValueError("max_paths 必须 >= 1")
    paths: list[Path] = []
    truncated = False
    stack: list[tuple[int, tuple]] = [(graph.start, ())]
    while stack:
        node, acc = stack.pop()
        if node == graph.end:
            paths.append(Path(acc))
            if len(paths) >= max_paths:
                truncated = bool(stack)  # 栈里还有未探索分支 => 不完整
                break
            continue
        edges = graph.adj.get(node)
        if not edges:
            continue  # 死路（正常构造不会出现）
        for edge in reversed(edges):  # 保持确定性顺序：原词路径优先
            stack.append((edge.dst, acc + (edge,)))
    return paths, truncated


# ---------------------------------------------------------------- 位置倒排

class PositionalIndex:
    """位置倒排：词元 -> {文档: [位置, ...]}。"""

    def __init__(self) -> None:
        self.postings: dict[str, dict[str, list[int]]] = defaultdict(
            lambda: defaultdict(list))
        self.doc_tokens: dict[str, list[str]] = {}

    def add_document(self, doc_id: str, text: str) -> None:
        tokens = tokenize(text)
        self.doc_tokens[doc_id] = tokens
        for pos, tok in enumerate(tokens):
            self.postings[tok][doc_id].append(pos)

    def phrase_occurrences(self, terms: tuple[str, ...]) -> dict[str, list[int]]:
        """返回 {doc_id: [起始位置, ...]}，要求词元按位置 +1 连续出现。"""
        if not terms:
            return {}
        posting_lists = []
        for t in terms:
            p = self.postings.get(t)
            if not p:
                return {}
            posting_lists.append(p)
        common = set(posting_lists[0])
        for p in posting_lists[1:]:
            common &= p.keys()
        result = {}
        for doc in common:
            later = [set(p[doc]) for p in posting_lists[1:]]
            starts = [
                p0 for p0 in posting_lists[0][doc]
                if all((p0 + offset + 1) in later[offset]
                       for offset in range(len(later)))
            ]
            if starts:
                result[doc] = sorted(starts)
        return result


# ---------------------------------------------------------------- 检索

@dataclass(frozen=True)
class Match:
    """一条命中证据：原词 / 扩展来源可区分。"""

    doc_id: str
    start: int
    end: int  # 开区间
    terms: tuple[str, ...]
    weight: float
    is_original: bool
    path_index: int
    group_keys: tuple


@dataclass
class SearchResult:
    query_tokens: list[str]
    paths: list[Path]
    truncated: bool       # True => 路径预算耗尽，结果可能不完整
    max_paths: int
    hits: list[tuple[float, str]]        # [(score, doc_id)] 降序，<= k 条
    evidence: dict[str, list[Match]]     # doc_id -> 命中证据

    @property
    def complete(self) -> bool:
        return not self.truncated


def search(
    index: PositionalIndex,
    query: str,
    groups: SynonymGroups,
    k: int = 10,
    expansion_weight: float = DEFAULT_EXPANSION_WEIGHT,
    max_paths: int = DEFAULT_MAX_PATHS,
) -> SearchResult:
    """对查询图与位置倒排联合执行 TopK。"""
    query_tokens = tokenize(query)
    if not query_tokens:
        return SearchResult([], [], False, max_paths, [], {})
    graph = build_query_graph(query_tokens, groups, expansion_weight)
    paths, truncated = enumerate_paths(graph, max_paths)

    span_best: dict[str, dict[tuple[int, int], float]] = defaultdict(dict)
    evidence: dict[str, list[Match]] = defaultdict(list)
    for pi, path in enumerate(paths):
        for doc, starts in index.phrase_occurrences(path.terms).items():
            for s in starts:
                key = (s, s + path.length)
                w = path.weight
                evidence[doc].append(Match(
                    doc, s, s + path.length, path.terms, w,
                    path.is_original, pi, path.group_keys))
                # 同区间只留最高权重：共享扩展不重复计分
                if w > span_best[doc].get(key, 0.0):
                    span_best[doc][key] = w

    scores = {}
    for doc, spans in span_best.items():
        miss = 1.0
        for w in spans.values():
            miss *= (1.0 - w)  # 概率和：有界，不无界叠分
        scores[doc] = 1.0 - miss

    best = heapq.nlargest(k, scores.items(), key=lambda kv: (kv[1], kv[0]))
    top = [(score, doc) for doc, score in best]
    return SearchResult(query_tokens, paths, truncated, max_paths,
                        top, dict(evidence))
