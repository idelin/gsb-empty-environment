"""过滤条件：字段比较 + 文本 token 匹配，支持 & | ~ 组合。

过滤在归并过程中对流式读出的候选逐条判定，未通过的候选被丢弃后
会继续从对应段读取下一条补足，而不是预先取各段前 K 条原始项。
"""

from __future__ import annotations

from typing import Any, Callable, Iterable

from .tokenizer import token_set, tokenize


class Filter:
    """包装一个 Document -> bool 的谓词，支持 & | ~ 组合。"""

    def __init__(self, predicate: Callable[[Any], bool], label: str = "?"):
        self._predicate = predicate
        self.label = label

    def __call__(self, doc: Any) -> bool:
        return self._predicate(doc)

    def __and__(self, other: "Filter") -> "Filter":
        return Filter(lambda d: self(d) and other(d), f"({self.label} & {other.label})")

    def __or__(self, other: "Filter") -> "Filter":
        return Filter(lambda d: self(d) or other(d), f"({self.label} | {other.label})")

    def __invert__(self) -> "Filter":
        return Filter(lambda d: not self(d), f"~{self.label}")

    def __repr__(self) -> str:
        return f"Filter({self.label})"


class Field:
    """字段引用：用比较运算符直接构造 Filter。

    示例：
        (Field("lang") == "zh") & Field("body").contains_token("索引")
        Field("score") >= 60
        Field("deleted_at").is_null()
    """

    def __init__(self, name: str):
        self.name = name

    def _cmp(self, op: str, func: Callable[[Any, Any], bool], value: Any) -> Filter:
        return Filter(
            lambda d: (v := d.get(self.name)) is not None and func(v, value),
            f"{self.name} {op} {value!r}",
        )

    def __eq__(self, value: Any) -> Filter:  # type: ignore[override]
        if value is None:
            return self.is_null()
        return self._cmp("==", lambda a, b: a == b, value)

    def __ne__(self, value: Any) -> Filter:  # type: ignore[override]
        if value is None:
            return self.not_null()
        return self._cmp("!=", lambda a, b: a != b, value)

    def __lt__(self, value: Any) -> Filter:
        return self._cmp("<", lambda a, b: a < b, value)

    def __le__(self, value: Any) -> Filter:
        return self._cmp("<=", lambda a, b: a <= b, value)

    def __gt__(self, value: Any) -> Filter:
        return self._cmp(">", lambda a, b: a > b, value)

    def __ge__(self, value: Any) -> Filter:
        return self._cmp(">=", lambda a, b: a >= b, value)

    def isin(self, values: Iterable[Any]) -> Filter:
        vals = frozenset(values)
        return Filter(
            lambda d: d.get(self.name) in vals,
            f"{self.name} IN {sorted(vals)!r}",
        )

    def is_null(self) -> Filter:
        return Filter(lambda d: d.get(self.name) is None, f"{self.name} IS NULL")

    def not_null(self) -> Filter:
        return Filter(lambda d: d.get(self.name) is not None, f"{self.name} IS NOT NULL")

    def contains_token(self, term: str) -> Filter:
        """文本字段经统一分词后包含指定 token（查询词同样走分词流程）。"""
        tokens = tokenize(term)
        if not tokens:
            return Filter(lambda d: False, f"{self.name} CONTAINS <empty>")
        wanted = frozenset(tokens)
        return Filter(
            lambda d: wanted <= token_set(d.get(self.name)),
            f"{self.name} CONTAINS {sorted(wanted)!r}",
        )
