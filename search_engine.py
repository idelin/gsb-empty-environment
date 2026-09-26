"""纯标准库实现的布尔检索引擎。

功能：
- 查询解析：AND / OR / NOT / 括号 / 字段限定（field:term、field:(...)）
- 优先级：NOT > AND > OR，相邻词项隐式 AND
- NOT 基于当前文档全集（删除的文档会从全集中移除）
- 结果携带命中证据（字段、词元、出现次数）
- 缺失字段不等于空串：缺失字段上的字段限定谓词为假，而不是按空串参与匹配

分词规则（索引与查询共用同一套规则）：
1. 文本一律按 UTF-8 解码（str）。
2. ASCII 字母与数字的连续序列 [A-Za-z0-9]+ 构成一个词元，统一转小写。
3. 每个 CJK 表意字符（汉字、平/片假名、韩文音节）单独成为一个单字词元，
   不做词典分词。
4. 其余所有字符（空白、标点、符号、下划线等）均为分隔符，不产生词元。
"""

from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass, field as dataclass_field
from enum import Enum, auto


# ---------------------------------------------------------------------------
# 分词
# ---------------------------------------------------------------------------

def _is_cjk(char: str) -> bool:
    """判断字符是否属于 CJK 表意/音节文字（按 Unicode 字符名识别）。"""
    name = unicodedata.name(char, "")
    return (
        "CJK UNIFIED" in name
        or "CJK COMPATIBILITY IDEOGRAPH" in name
        or "HIRAGANA" in name
        or "KATAKANA" in name
        or "HANGUL SYLLABLE" in name
    )


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的规则把文本切分为词元列表。"""
    tokens: list[str] = []
    buffer: list[str] = []
    for char in text:
        if char.isascii() and char.isalnum():
            buffer.append(char.lower())
        elif _is_cjk(char):
            if buffer:
                tokens.append("".join(buffer))
                buffer.clear()
            tokens.append(char)
        else:
            if buffer:
                tokens.append("".join(buffer))
                buffer.clear()
    if buffer:
        tokens.append("".join(buffer))
    return tokens


# ---------------------------------------------------------------------------
# 查询词法分析
# ---------------------------------------------------------------------------

class TokenType(Enum):
    TERM = auto()
    AND = auto()
    OR = auto()
    NOT = auto()
    LPAREN = auto()
    RPAREN = auto()
    COLON = auto()
    EOF = auto()


@dataclass(frozen=True)
class Token:
    type: TokenType
    value: str = ""


_QUERY_TOKEN_RE = re.compile(
    r"""
    \s*(?:
        (?P<lparen>\()
      | (?P<rparen>\))
      | (?P<colon>:)
      | (?P<term>[^\s():]+)
    )
    """,
    re.VERBOSE,
)

_KEYWORDS = {"and": TokenType.AND, "or": TokenType.OR, "not": TokenType.NOT}


class QuerySyntaxError(ValueError):
    """查询串不符合语法时抛出。"""


def lex_query(query: str) -> list[Token]:
    """把查询串切成 Token 序列；AND/OR/NOT 关键字大小写不敏感。"""
    tokens: list[Token] = []
    position = 0
    while position < len(query):
        if query[position:].strip() == "":
            break
        match = _QUERY_TOKEN_RE.match(query, position)
        if match is None or match.end() == position:
            raise QuerySyntaxError(f"无法识别的字符，位置 {position}: {query[position]!r}")
        position = match.end()
        if match.lastgroup is None:
            continue
        kind = match.lastgroup
        text = match.group(kind)
        if kind == "lparen":
            tokens.append(Token(TokenType.LPAREN))
        elif kind == "rparen":
            tokens.append(Token(TokenType.RPAREN))
        elif kind == "colon":
            tokens.append(Token(TokenType.COLON))
        else:
            keyword = _KEYWORDS.get(text.lower())
            if keyword is not None:
                tokens.append(Token(keyword, text))
            else:
                tokens.append(Token(TokenType.TERM, text))
    tokens.append(Token(TokenType.EOF))
    return tokens


# ---------------------------------------------------------------------------
# 查询语法分析（递归下降）
# ---------------------------------------------------------------------------
# 文法（优先级 NOT > AND > OR，相邻词项隐式 AND）：
#   query   := or_expr EOF
#   or_expr := and_expr (OR and_expr)*
#   and_expr:= unary ((AND)? unary)*
#   unary   := NOT unary | primary
#   primary := '(' or_expr ')' | TERM ':' primary | TERM


class Node:
    """AST 基类。"""


@dataclass(frozen=True)
class Term(Node):
    value: str


@dataclass(frozen=True)
class Field(Node):
    name: str
    child: Node


@dataclass(frozen=True)
class And(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Or(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Not(Node):
    child: Node


class Parser:
    def __init__(self, tokens: list[Token]):
        self._tokens = tokens
        self._pos = 0

    def _peek(self) -> Token:
        return self._tokens[self._pos]

    def _advance(self) -> Token:
        token = self._tokens[self._pos]
        self._pos += 1
        return token

    def _expect(self, token_type: TokenType, what: str) -> Token:
        token = self._peek()
        if token.type is not token_type:
            raise QuerySyntaxError(f"期望 {what}，实际得到 {token.type.name} ({token.value!r})")
        return self._advance()

    def parse(self) -> Node:
        node = self._or_expr()
        self._expect(TokenType.EOF, "查询结束")
        return node

    def _or_expr(self) -> Node:
        node = self._and_expr()
        while self._peek().type is TokenType.OR:
            self._advance()
            node = Or(node, self._and_expr())
        return node

    def _and_expr(self) -> Node:
        node = self._unary()
        # 显式 AND 或相邻词项的隐式 AND
        while self._peek().type in (
            TokenType.AND, TokenType.TERM, TokenType.NOT, TokenType.LPAREN,
        ):
            if self._peek().type is TokenType.AND:
                self._advance()
            node = And(node, self._unary())
        return node

    def _unary(self) -> Node:
        if self._peek().type is TokenType.NOT:
            self._advance()
            return Not(self._unary())
        return self._primary()

    def _primary(self) -> Node:
        token = self._peek()
        if token.type is TokenType.LPAREN:
            self._advance()
            node = self._or_expr()
            self._expect(TokenType.RPAREN, "右括号 )")
            return node
        if token.type is TokenType.TERM:
            self._advance()
            if self._peek().type is TokenType.COLON:
                self._advance()
                return Field(token.value, self._primary())
            return Term(token.value)
        raise QuerySyntaxError(f"期望词项或左括号，实际得到 {token.type.name} ({token.value!r})")


def parse_query(query: str) -> Node:
    """解析查询串为 AST；空查询或语法错误抛 QuerySyntaxError。"""
    tokens = lex_query(query)
    if tokens[0].type is TokenType.EOF:
        raise QuerySyntaxError("空查询")
    return Parser(tokens).parse()


# ---------------------------------------------------------------------------
# 命中证据与结果
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Evidence:
    """一条命中证据：某字段中命中了某词元及其出现次数。"""

    field: str
    term: str
    count: int


@dataclass
class SearchResult:
    doc_id: str
    evidence: list[Evidence] = dataclass_field(default_factory=list)


# ---------------------------------------------------------------------------
# 检索引擎
# ---------------------------------------------------------------------------

class SearchEngine:
    """基于倒排索引的布尔检索引擎。

    - 文档为 doc_id -> {字段名: UTF-8 文本}
    - NOT 对“当前全集”（未删除的全部文档）取补集
    - 缺失字段上的字段限定谓词为假（缺失 != 空串）
    """

    def __init__(self) -> None:
        self._docs: dict[str, dict[str, str]] = {}
        # 倒排索引：字段 -> 词元 -> {doc_id: 出现次数}
        self._index: dict[str, dict[str, dict[str, int]]] = {}

    # -- 文档管理 ----------------------------------------------------------

    def add_document(self, doc_id: str, fields: dict[str, str]) -> None:
        """加入文档；doc_id 已存在时先删除再重建（即覆盖）。"""
        if doc_id in self._docs:
            self.delete_document(doc_id)
        self._docs[doc_id] = dict(fields)
        for field_name, text in fields.items():
            counts: dict[str, int] = {}
            for token in tokenize(text):
                counts[token] = counts.get(token, 0) + 1
            postings = self._index.setdefault(field_name, {})
            for token, count in counts.items():
                postings.setdefault(token, {})[doc_id] = count

    def delete_document(self, doc_id: str) -> None:
        """删除文档，同时从全集中移除（NOT 的补集不再包含它）。"""
        fields = self._docs.pop(doc_id, None)
        if fields is None:
            raise KeyError(f"文档不存在: {doc_id!r}")
        for field_name, text in fields.items():
            postings = self._index.get(field_name, {})
            for token in set(tokenize(text)):
                posting = postings.get(token)
                if posting is not None:
                    posting.pop(doc_id, None)

    @property
    def universe(self) -> set[str]:
        """当前文档全集（NOT 的补集基准）。"""
        return set(self._docs)

    def document_count(self) -> int:
        return len(self._docs)

    # -- 查询 --------------------------------------------------------------

    def search(self, query: str) -> list[SearchResult]:
        """执行查询，返回按 doc_id 排序、带命中证据的结果列表。"""
        node = parse_query(query)
        matched = self._eval(node, field_override=None)
        results = []
        for doc_id in sorted(matched):
            evidence = sorted(set(matched[doc_id]), key=lambda e: (e.field, e.term))
            results.append(SearchResult(doc_id=doc_id, evidence=evidence))
        return results

    # -- 求值（返回 doc_id -> 证据列表） ------------------------------------

    def _eval(self, node: Node, field_override: str | None) -> dict[str, list[Evidence]]:
        if isinstance(node, Term):
            return self._eval_term(node.value, field_override)
        if isinstance(node, Field):
            # 字段限定向下传播到子树内所有词项
            return self._eval(node.child, field_override=node.name)
        if isinstance(node, And):
            left = self._eval(node.left, field_override)
            right = self._eval(node.right, field_override)
            return {
                doc_id: left[doc_id] + right[doc_id]
                for doc_id in left.keys() & right.keys()
            }
        if isinstance(node, Or):
            left = self._eval(node.left, field_override)
            right = self._eval(node.right, field_override)
            merged = dict(left)
            for doc_id, evidence in right.items():
                merged.setdefault(doc_id, []).extend(evidence)
            return merged
        if isinstance(node, Not):
            child = self._eval(node.child, field_override)
            # 纯否定分支本身不产生正向命中证据
            return {doc_id: [] for doc_id in self.universe - child.keys()}
        raise TypeError(f"未知节点: {node!r}")

    def _eval_term(self, value: str, field_name: str | None) -> dict[str, list[Evidence]]:
        tokens = tokenize(value)
        if not tokens:
            # 词项经分词后没有任何词元（例如全是标点），不匹配任何文档
            return {}
        if field_name is not None:
            return self._eval_term_in_field(tokens, field_name)
        return self._eval_term_any_field(tokens)

    def _eval_term_in_field(self, tokens: list[str], field_name: str) -> dict[str, list[Evidence]]:
        """字段限定：所有词元都必须出现在该字段中；字段缺失即不匹配。"""
        postings = self._index.get(field_name)
        if postings is None:
            return {}
        per_token = [postings.get(token, {}) for token in tokens]
        common = set(per_token[0])
        for posting in per_token[1:]:
            common &= posting.keys()
        return {
            doc_id: [
                Evidence(field=field_name, term=token, count=posting[doc_id])
                for token, posting in zip(tokens, per_token)
            ]
            for doc_id in common
        }

    def _eval_term_any_field(self, tokens: list[str]) -> dict[str, list[Evidence]]:
        """无字段限定：每个词元须出现在文档的至少一个字段中（不要求同字段）。"""
        per_token: list[dict[str, list[Evidence]]] = []
        for token in tokens:
            occurrences: dict[str, list[Evidence]] = {}
            for field_name, postings in self._index.items():
                for doc_id, count in postings.get(token, {}).items():
                    occurrences.setdefault(doc_id, []).append(
                        Evidence(field=field_name, term=token, count=count)
                    )
            per_token.append(occurrences)
        common = set(per_token[0])
        for occurrences in per_token[1:]:
            common &= occurrences.keys()
        return {
            doc_id: [ev for occurrences in per_token for ev in occurrences[doc_id]]
            for doc_id in common
        }
