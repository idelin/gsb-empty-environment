"""boolsearch — 纯标准库的布尔检索引擎（Python 3.11+）。

功能：
  * 解析 AND / OR / NOT / 括号 / 字段限定（field:term）
  * 优先级：NOT > AND > OR，因此 ``NOT A OR B`` 解析为 ``(NOT A) OR B``
  * NOT 基于“固定文档全集”求补集；纯否定查询（如 ``NOT python``）可正确执行
  * 删除文档会同时从全集与索引中移除
  * 结果带命中证据（字段、词项、字符偏移、上下文片段）
  * 缺失字段不等于空字符串：对缺失字段的查询永不命中

分词规则（tokenize）：
  1. 文本先按原样扫描，词项再做 casefold（大小写折叠）归一化；
  2. CJK 字符（中日韩表意文字、日文假名、韩文音节）逐字成词；
  3. 其余连续的字母 / 数字 / 下划线构成一个词；
  4. 其他字符（空白、标点等）一律视为分隔符；
  5. 每个词记录其在原文中的字符偏移 [start, end)，用于命中证据。

查询语法（EBNF）：
  query   := or_expr
  or_expr := and_expr (OR and_expr)*
  and_expr:= not_expr (AND not_expr)*
  not_expr:= NOT not_expr | primary
  primary := '(' or_expr ')' | [FIELD ':'] TERM

  * AND / OR / NOT 必须大写，小写形式按普通词项处理；
  * TERM 为不含空白、括号、冒号的非空字符串，查询时同样做 casefold；
  * FIELD 与文档字段名精确匹配（区分大小写）。
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field as dataclass_field


# ---------------------------------------------------------------------------
# 分词
# ---------------------------------------------------------------------------

def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return (
        0x4E00 <= cp <= 0x9FFF      # CJK 统一表意文字
        or 0x3400 <= cp <= 0x4DBF   # 扩展 A
        or 0xF900 <= cp <= 0xFAFF   # 兼容表意文字
        or 0x3040 <= cp <= 0x30FF   # 日文平/片假名
        or 0xAC00 <= cp <= 0xD7AF   # 韩文音节
    )


def tokenize(text: str) -> list[tuple[str, int, int]]:
    """按模块 docstring 中的规则分词，返回 (词项, start, end) 列表。"""
    tokens: list[tuple[str, int, int]] = []
    i, n = 0, len(text)
    while i < n:
        ch = text[i]
        if _is_cjk(ch):
            tokens.append((ch.casefold(), i, i + 1))
            i += 1
        elif ch.isalnum() or ch == "_":
            j = i + 1
            while j < n and not _is_cjk(text[j]) and (text[j].isalnum() or text[j] == "_"):
                j += 1
            tokens.append((text[i:j].casefold(), i, j))
            i = j
        else:
            i += 1
    return tokens


# ---------------------------------------------------------------------------
# 查询解析
# ---------------------------------------------------------------------------

class QuerySyntaxError(ValueError):
    """查询串语法错误。"""


_TOKEN_RE = re.compile(r"\s*(?:(?P<lp>\()|(?P<rp>\))|(?P<colon>:)|(?P<word>[^\s():]+))")


def _lex(query: str) -> list[tuple[str, str, int]]:
    items: list[tuple[str, str, int]] = []
    pos = 0
    while pos < len(query):
        m = _TOKEN_RE.match(query, pos)
        if not m:
            raise QuerySyntaxError(f"位置 {pos} 处存在无法识别的字符: {query[pos]!r}")
        pos = m.end()
        if m.lastgroup == "lp":
            items.append(("LPAREN", "(", m.start("lp")))
        elif m.lastgroup == "rp":
            items.append(("RPAREN", ")", m.start("rp")))
        elif m.lastgroup == "colon":
            items.append(("COLON", ":", m.start("colon")))
        elif m.lastgroup == "word":
            word = m.group("word")
            if word in ("AND", "OR", "NOT"):
                items.append((word, word, m.start("word")))
            else:
                items.append(("WORD", word, m.start("word")))
    items.append(("EOF", "", len(query)))
    return items


# --- AST -------------------------------------------------------------------

class Node:
    def evaluate(self, index: "Index") -> set[str]:
        raise NotImplementedError

    def evidence(self, index: "Index", doc_id: str) -> list["Evidence"]:
        raise NotImplementedError


@dataclass(frozen=True)
class Term(Node):
    value: str            # 已 casefold 的词项
    field: str | None = None  # None 表示不限定字段

    def evaluate(self, index: "Index") -> set[str]:
        return index._term_docs(self.field, self.value)

    def evidence(self, index: "Index", doc_id: str) -> list["Evidence"]:
        return index._term_evidence(doc_id, self.field, self.value)


@dataclass(frozen=True)
class And(Node):
    left: Node
    right: Node

    def evaluate(self, index: "Index") -> set[str]:
        return self.left.evaluate(index) & self.right.evaluate(index)

    def evidence(self, index: "Index", doc_id: str) -> list["Evidence"]:
        return self.left.evidence(index, doc_id) + self.right.evidence(index, doc_id)


@dataclass(frozen=True)
class Or(Node):
    left: Node
    right: Node

    def evaluate(self, index: "Index") -> set[str]:
        return self.left.evaluate(index) | self.right.evaluate(index)

    def evidence(self, index: "Index", doc_id: str) -> list["Evidence"]:
        out: list[Evidence] = []
        if doc_id in self.left.evaluate(index):
            out += self.left.evidence(index, doc_id)
        if doc_id in self.right.evaluate(index):
            out += self.right.evidence(index, doc_id)
        return out


@dataclass(frozen=True)
class Not(Node):
    child: Node

    def evaluate(self, index: "Index") -> set[str]:
        # NOT 基于固定文档全集求补集
        return set(index.universe) - self.child.evaluate(index)

    def evidence(self, index: "Index", doc_id: str) -> list["Evidence"]:
        # 否定分支不产生正向命中证据
        return []


class Parser:
    """递归下降解析器；优先级 NOT > AND > OR。"""

    def __init__(self, query: str):
        self._tokens = _lex(query)
        self._pos = 0

    def _peek(self) -> tuple[str, str, int]:
        return self._tokens[self._pos]

    def _next(self) -> tuple[str, str, int]:
        tok = self._tokens[self._pos]
        self._pos += 1
        return tok

    def parse(self) -> Node:
        node = self._parse_or()
        kind, _, pos = self._peek()
        if kind != "EOF":
            raise QuerySyntaxError(f"位置 {pos} 处有多余内容: {self._peek()[1]!r}")
        return node

    def _parse_or(self) -> Node:
        node = self._parse_and()
        while self._peek()[0] == "OR":
            self._next()
            node = Or(node, self._parse_and())
        return node

    def _parse_and(self) -> Node:
        node = self._parse_not()
        while self._peek()[0] == "AND":
            self._next()
            node = And(node, self._parse_not())
        return node

    def _parse_not(self) -> Node:
        if self._peek()[0] == "NOT":
            self._next()
            return Not(self._parse_not())
        return self._parse_primary()

    def _parse_primary(self) -> Node:
        kind, value, pos = self._peek()
        if kind == "LPAREN":
            self._next()
            node = self._parse_or()
            if self._peek()[0] != "RPAREN":
                raise QuerySyntaxError(f"位置 {pos} 处的 '(' 缺少匹配的 ')'")
            self._next()
            return node
        if kind == "WORD":
            self._next()
            if self._peek()[0] == "COLON":
                self._next()
                tkind, tvalue, tpos = self._peek()
                if tkind != "WORD":
                    raise QuerySyntaxError(f"位置 {tpos} 处字段 {value!r} 后缺少词项")
                self._next()
                return Term(tvalue.casefold(), field=value)
            return Term(value.casefold())
        raise QuerySyntaxError(f"位置 {pos} 处期望词项或 '('，实际为 {value!r}")


def parse(query: str) -> Node:
    return Parser(query).parse()


# ---------------------------------------------------------------------------
# 证据
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Evidence:
    doc_id: str
    field: str
    term: str
    start: int
    end: int
    snippet: str


def _make_snippet(text: str, start: int, end: int, window: int = 20) -> str:
    lo = max(0, start - window)
    hi = min(len(text), end + window)
    prefix = "…" if lo > 0 else ""
    suffix = "…" if hi < len(text) else ""
    return f"{prefix}{text[lo:start]}【{text[start:end]}】{text[end:hi]}{suffix}"


# ---------------------------------------------------------------------------
# 索引
# ---------------------------------------------------------------------------

@dataclass
class Document:
    doc_id: str
    fields: dict[str, str] = dataclass_field(default_factory=dict)


class Index:
    """内存倒排索引。universe 为当前全部文档 ID（固定全集，随增删变化）。"""

    def __init__(self) -> None:
        self._docs: dict[str, Document] = {}
        # doc_id -> field -> [(token, start, end), ...]
        self._tokens: dict[str, dict[str, list[tuple[str, int, int]]]] = {}
        # (field, term) -> {doc_id: [start, ...]}
        self._postings: dict[tuple[str, str], dict[str, list[int]]] = {}

    # -- 文档管理 -----------------------------------------------------------

    def add_document(self, doc_id: str, fields: dict[str, str]) -> None:
        if doc_id in self._docs:
            raise ValueError(f"文档已存在: {doc_id!r}")
        # 缺失字段不等于空字符串：只保存显式给出的字段
        self._docs[doc_id] = Document(doc_id, dict(fields))
        per_field: dict[str, list[tuple[str, int, int]]] = {}
        for fname, text in fields.items():
            toks = tokenize(text)
            per_field[fname] = toks
            for tok, start, _ in toks:
                self._postings.setdefault((fname, tok), {}).setdefault(doc_id, []).append(start)
        self._tokens[doc_id] = per_field

    def delete_document(self, doc_id: str) -> None:
        if doc_id not in self._docs:
            raise KeyError(f"文档不存在: {doc_id!r}")
        for fname, toks in self._tokens[doc_id].items():
            for tok, _, _ in toks:
                docs = self._postings[(fname, tok)]
                del docs[doc_id]
                if not docs:
                    del self._postings[(fname, tok)]
        del self._tokens[doc_id]
        del self._docs[doc_id]  # 从全集移除

    @property
    def universe(self) -> frozenset[str]:
        return frozenset(self._docs)

    # -- 查询支持 -------------------------------------------------------------

    def _term_docs(self, field: str | None, term: str) -> set[str]:
        if field is not None:
            # 字段不存在于任何文档时，postings 中无此键 → 空集
            return set(self._postings.get((field, term), {}))
        out: set[str] = set()
        for (fname, t), docs in self._postings.items():
            if t == term:
                out |= set(docs)
        return out

    def _term_evidence(self, doc_id: str, field: str | None, term: str) -> list[Evidence]:
        per_field = self._tokens.get(doc_id)
        if per_field is None:
            return []
        out: list[Evidence] = []
        fields = [field] if field is not None else sorted(per_field)
        for fname in fields:
            toks = per_field.get(fname)
            if toks is None:
                continue  # 缺失字段：无证据，也不视为空串
            text = self._docs[doc_id].fields[fname]
            for tok, start, end in toks:
                if tok == term:
                    out.append(Evidence(doc_id, fname, term, start, end,
                                        _make_snippet(text, start, end)))
        return out

    # -- 对外查询 -------------------------------------------------------------

    def search(self, query: str) -> "SearchResult":
        ast = parse(query)
        hits = ast.evaluate(self)
        evidence = {doc_id: ast.evidence(self, doc_id) for doc_id in sorted(hits)}
        return SearchResult(query, sorted(hits), evidence)


@dataclass
class SearchResult:
    query: str
    doc_ids: list[str]
    evidence: dict[str, list[Evidence]]
