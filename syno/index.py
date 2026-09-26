"""文档与位置倒排索引（纯标准库实现，非全文检索库）。

索引内容：
- postings[word] = {doc_id: [token 位置...]}
- 文档同时保存 token 与字符偏移，用于证据回显。
短语位置通过 postings 做连续位置核验：先取各组成词的文档候选，
按最小 df 的词优先求交，再逐位置核验连续性。
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .tokenizer import Token, tokenize


@dataclass
class Document:
    doc_id: str
    text: str
    tokens: list[Token] = field(default_factory=list)

    @property
    def length(self) -> int:
        return len(self.tokens)


class PositionalIndex:
    def __init__(self) -> None:
        self.docs: dict[str, Document] = {}
        self.postings: dict[str, dict[str, list[int]]] = {}

    def add_document(self, doc_id: str, text: str) -> Document:
        if doc_id in self.docs:
            raise ValueError(f"文档 id 重复：{doc_id}")
        doc = Document(doc_id, text, tokenize(text))
        self.docs[doc_id] = doc
        for tok in doc.tokens:
            self.postings.setdefault(tok.text, {}).setdefault(doc_id, [])
            self.postings[tok.text][doc_id].append(tok.pos)
        return doc

    def add_text_file(self, doc_id: str, path: str) -> Document:
        with open(path, "r", encoding="utf-8") as f:
            return self.add_document(doc_id, f.read())

    def doc_count(self) -> int:
        return len(self.docs)

    def doc_frequency(self, word: str) -> int:
        return len(self.postings.get(word, {}))

    def term_positions(self, doc_id: str, word: str) -> list[int]:
        return list(self.postings.get(word, {}).get(doc_id, ()))

    def docs_with_any(self, words) -> list[str]:
        hit: set[str] = set()
        for w in words:
            hit.update(self.postings.get(w, {}))
        return sorted(hit)

    def phrase_positions(self, doc_id: str, phrase_tokens) -> list[int]:
        """返回文档中与 phrase_tokens 连续精确匹配的起始 token 位置。"""
        ptoks = tuple(phrase_tokens)
        if not ptoks:
            return []
        if doc_id not in self.docs:
            return []
        per_word = [self.postings.get(w, {}).get(doc_id) for w in ptoks]
        if any(p is None for p in per_word):
            return []
        # 以最稀疏词的位置为锚点核验连续性
        anchor = min(range(len(per_word)), key=lambda i: len(per_word[i]))
        anchor_positions = per_word[anchor]
        sets = [set(p) for p in per_word]
        results: list[int] = []
        for pos in anchor_positions:
            start = pos - anchor
            if start < 0:
                continue
            if all(start + k in sets[k] for k in range(len(ptoks)) if k != anchor):
                results.append(start)
        return results

    def offset_of(self, doc_id: str, pos: int) -> tuple[int, int]:
        tok = self.docs[doc_id].tokens[pos]
        return tok.start, tok.end

    def text_at(self, doc_id: str, start_pos: int, length: int) -> tuple[str, int, int]:
        doc = self.docs[doc_id]
        st = doc.tokens[start_pos].start
        en = doc.tokens[start_pos + length - 1].end
        return doc.text[st:en], st, en
