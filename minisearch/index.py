"""内存倒排索引：term -> {doc_id: [positions]}。仅使用标准库。"""

from .tokenizer import tokenize


class Index:
    def __init__(self) -> None:
        self._docs: dict[str, list[str]] = {}
        self._postings: dict[str, dict[str, list[int]]] = {}

    def add_document(self, doc_id: str, text: str) -> None:
        if doc_id in self._docs:
            raise ValueError(f"重复文档 id: {doc_id}")
        tokens = tokenize(text)
        self._docs[doc_id] = tokens
        for pos, term in enumerate(tokens):
            self._postings.setdefault(term, {}).setdefault(doc_id, []).append(pos)

    @property
    def num_docs(self) -> int:
        return len(self._docs)

    @property
    def doc_ids(self) -> list[str]:
        return sorted(self._docs)

    @property
    def vocabulary(self) -> list[str]:
        return sorted(self._postings)

    def postings(self, term: str) -> dict[str, list[int]]:
        """返回 term 的 {doc_id: [positions]}；不存在时返回空 dict。"""
        return self._postings.get(term, {})

    def posting_size(self, term: str) -> int:
        """(doc, positions) 条目总数，用于成本估算。"""
        return len(self._postings.get(term, {}))
