"""显式同义知识库。

两类同义严格分开存储，互不混用：
- 单词同义 (word group)：同组内所有单词构成完全团 (clique)，
  即添加 {"a","b"} 会同时得到 a->b 和 b->a（a↔b）。
- 短语同义 (phrase group)：整条多词短语作为不可拆散的替换单元，
  组内各短语互相同义，匹配时必须连续整段命中。

边权规则：
- 默认边权 DEFAULT_EDGE_WEIGHT=0.7；显式 pair 可自定义权重。
- 权重语义：沿边传播时相乘（路径权重 = 各边权乘积）。
- 0 < weight <= 1；权重越高表示同义越可信。
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field

from .tokenizer import tokenize

DEFAULT_EDGE_WEIGHT = 0.7


@dataclass
class Phrase:
    surface: str
    tokens: tuple[str, ...]

    @property
    def length(self) -> int:
        return len(self.tokens)


@dataclass
class _PhraseGroup:
    gid: int
    weight: float
    phrases: list[Phrase] = field(default_factory=list)


class SynonymStore:
    def __init__(self, default_weight: float = DEFAULT_EDGE_WEIGHT) -> None:
        self._adj: dict[str, dict[str, float]] = defaultdict(dict)
        self._phrase_groups: list[_PhraseGroup] = []
        self._default_weight = default_weight

    @staticmethod
    def _check_weight(weight: float) -> float:
        if not (0.0 < weight <= 1.0):
            raise ValueError(f"边权必须在 (0, 1]，收到 {weight}")
        return float(weight)

    def add_word_group(self, words, weight: float | None = None) -> None:
        """添加显式单词同义组：组内两两连无向边（完全团）。

        重复添加 {"a","b"} 只保留一条边（取最大权重），不会产生多重边，
        因此 a↔b 不会因为重复声明而被“展开两次”。
        """
        w = self._default_weight if weight is None else self._check_weight(weight)
        terms = [t.casefold().strip() for t in words if t.strip()]
        if len(terms) < 2:
            return
        for i in range(len(terms)):
            for j in range(i + 1, len(terms)):
                self._link(terms[i], terms[j], w)

    def add_word_pair(self, a: str, b: str, weight: float | None = None) -> None:
        """添加显式双向同义对 a↔b。"""
        w = self._default_weight if weight is None else self._check_weight(weight)
        self._link(a.casefold().strip(), b.casefold().strip(), w)

    def _link(self, a: str, b: str, w: float) -> None:
        if not a or not b or a == b:
            return
        old = self._adj[a].get(b)
        self._adj[a][b] = w if old is None else max(old, w)
        self._adj[b][a] = self._adj[a][b]

    def add_phrase_group(self, phrases, weight: float | None = None) -> None:
        """添加多词（或多字）短语同义组。

        短语按同一套分词器切分；只含 1 个 token 的短语会被拒绝，
        以保持“短语同义与单词同义分开”。组内短语必须两两等长吗？不要求：
        “new york(2)↔nyc(1)”这类长短语↔单词替换允许存在，
        但它仍属于短语层，单词 nyc 不会因此获得任何单词层扩展边。
        """
        w = self._default_weight if weight is None else self._check_weight(weight)
        parsed: list[Phrase] = []
        seen: set[tuple[str, ...]] = set()
        for raw in phrases:
            toks = tuple(t.text for t in tokenize(raw))
            if len(toks) < 1:
                raise ValueError(f"空短语：{raw!r}")
            if toks in seen:
                continue
            seen.add(toks)
            parsed.append(Phrase(raw.strip(), toks))
        if len(parsed) < 2:
            raise ValueError("短语同义组至少需要 2 条不同短语")
        # 组内至少有一条多词短语才属于“短语层”；单 token 成员（如
        # kicked the bucket ↔ died 中的 died）不会进入单词同义图。
        if not any(len(p.tokens) >= 2 for p in parsed):
            raise ValueError("短语同义组至少需要一条多词（>=2 token）短语")
        self._phrase_groups.append(
            _PhraseGroup(len(self._phrase_groups), w, parsed)
        )

    def neighbors(self, word: str) -> dict[str, float]:
        return dict(self._adj.get(word.casefold(), {}))

    def edge_weight(self, a: str, b: str) -> float | None:
        return self._adj.get(a.casefold(), {}).get(b.casefold())

    def phrase_groups(self) -> list[_PhraseGroup]:
        return list(self._phrase_groups)

    def iter_phrase_alternatives(self, tokens):
        """给定 token 文本序列（通常是查询），枚举其中出现的注册短语及组内替代。

        产出 (start, end, gid, group_weight, phrase_list)。
        匹配采用“左端点优先、最长优先”，同一区间只命中一次；
        被更长短语整体覆盖的 token 不再参与更短短语，避免交叉重复。
        """
        seq = tuple(tokens)
        n = len(seq)
        blocked_until = 0
        for start in range(n):
            if start < blocked_until:
                continue
            matches = []
            for grp in self._phrase_groups:
                for ph in grp.phrases:
                    L = ph.length
                    if start + L <= n and seq[start:start + L] == ph.tokens:
                        matches.append((L, grp))
            if not matches:
                continue
            # 同一左端点最长优先；一旦命中，其覆盖区间内的左端点全部跳过，
            # 保证查询中的 token 区间不会被多条短语交叉重复使用。
            matches.sort(key=lambda x: (-x[0], x[1].gid))
            length, grp = matches[0]
            blocked_until = start + length
            yield start, start + length, grp.gid, grp.weight, grp.phrases
