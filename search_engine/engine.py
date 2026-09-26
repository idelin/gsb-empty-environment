"""分页查询引擎：固定索引版本 + 键集（keyset）分页。

核心保证：

- 每次查询/翻页都绑定一个固定的索引版本，版本内结果集不变，
  配合全序排序键 ``(-score, doc_id)`` 做到不重复、不漏项。
- 同分文档按 ``doc_id`` 字典序排列，形成确定性的全序。
- 游标内含查询摘要，换查询复用游标会被拒绝。
- 索引更新时由 ``UpdatePolicy`` 决定：保留旧快照（旧游标可继续
  翻页，读到的是更新前的数据）或明确失效（旧游标报错）。
"""

from __future__ import annotations

import enum
from collections import Counter
from dataclasses import dataclass, field

from .cursor import (
    CURSOR_STRUCTURE_VERSION,
    CursorIndexExpiredError,
    CursorQueryMismatchError,
    decode_cursor,
    encode_cursor,
    query_digest,
)
from .tokenizer import tokenize


class UpdatePolicy(enum.Enum):
    """索引更新时对旧版本快照的处理策略。"""

    SNAPSHOT = "snapshot"      # 保留旧快照：旧游标继续有效，读旧数据
    INVALIDATE = "invalidate"  # 明确失效：仅保留最新版本，旧游标报错


@dataclass(frozen=True)
class SearchHit:
    doc_id: str
    score: int


@dataclass(frozen=True)
class Page:
    hits: tuple[SearchHit, ...]
    next_cursor: str | None
    index_version: int


@dataclass(frozen=True)
class _IndexSnapshot:
    version: int
    postings: dict[str, dict[str, int]] = field(default_factory=dict)


class SearchEngine:
    """基于本地倒排索引的分页查询引擎。

    :param secret: 游标 HMAC 签名密钥；缺省使用内置演示密钥，
        生产环境应显式传入随机密钥。
    :param on_update: 索引更新策略，见 :class:`UpdatePolicy`。
    """

    def __init__(
        self,
        *,
        secret: bytes | None = None,
        on_update: UpdatePolicy = UpdatePolicy.INVALIDATE,
    ) -> None:
        self._secret = secret if secret is not None else b"search-engine-demo-secret"
        self._on_update = on_update
        self._docs: dict[str, str] = {}
        self._version = 0
        self._snapshots: dict[int, _IndexSnapshot] = {}
        self._rebuild()

    @property
    def version(self) -> int:
        return self._version

    def add_document(self, doc_id: str, text: str) -> int:
        """新增或覆盖一篇文档，返回新的索引版本号。"""
        if not isinstance(doc_id, str) or not doc_id:
            raise ValueError("doc_id 必须是非空字符串")
        self._docs[doc_id] = text
        return self._rebuild()

    def remove_document(self, doc_id: str) -> int:
        """删除一篇文档，返回新的索引版本号。"""
        if doc_id not in self._docs:
            raise KeyError(f"文档不存在: {doc_id!r}")
        del self._docs[doc_id]
        return self._rebuild()

    def _rebuild(self) -> int:
        self._version += 1
        postings: dict[str, dict[str, int]] = {}
        for doc_id in sorted(self._docs):
            for term, count in Counter(tokenize(self._docs[doc_id])).items():
                postings.setdefault(term, {})[doc_id] = count
        snapshot = _IndexSnapshot(version=self._version, postings=postings)
        if self._on_update is UpdatePolicy.INVALIDATE:
            self._snapshots = {self._version: snapshot}
        else:
            self._snapshots[self._version] = snapshot
        return self._version

    def search(
        self,
        query: str,
        *,
        page_size: int = 10,
        cursor: str | None = None,
    ) -> Page:
        """执行一次查询或翻页。

        查询语义为 AND：文档必须包含查询分词后的全部词项；
        得分为各词项词频之和；排序为 ``(-score, doc_id)`` 全序。
        """
        if page_size < 1:
            raise ValueError("page_size 必须 >= 1")
        terms = tokenize(query)
        if not terms:
            raise ValueError("查询分词后不含任何词项")
        digest = query_digest(terms)

        after_key: tuple[int, str] | None = None
        if cursor is not None:
            payload = decode_cursor(cursor, self._secret)
            if payload["query_digest"] != digest:
                raise CursorQueryMismatchError(
                    "游标绑定的查询摘要与当前查询不一致，禁止跨查询复用游标"
                )
            snapshot = self._snapshots.get(payload["index_version"])
            if snapshot is None:
                raise CursorIndexExpiredError(
                    f"游标绑定的索引版本 v{payload['index_version']} 已失效"
                )
            after_key = (payload["last_key"][0], payload["last_key"][1])
        else:
            snapshot = self._snapshots[self._version]

        candidates: set[str] | None = None
        for term in terms:
            doc_ids = set(snapshot.postings.get(term, ()))
            candidates = doc_ids if candidates is None else candidates & doc_ids
        assert candidates is not None

        scored: list[tuple[tuple[int, str], str, int]] = []
        for doc_id in candidates:
            score = sum(snapshot.postings[term][doc_id] for term in terms)
            scored.append(((-score, doc_id), doc_id, score))
        scored.sort(key=lambda item: item[0])

        if after_key is not None:
            after_sort_key = (-after_key[0], after_key[1])
            scored = [item for item in scored if item[0] > after_sort_key]

        page_items = scored[:page_size]
        has_more = len(scored) > page_size

        next_cursor = None
        if has_more and page_items:
            _, last_doc_id, last_score = page_items[-1]
            next_cursor = encode_cursor(
                {
                    "v": CURSOR_STRUCTURE_VERSION,
                    "index_version": snapshot.version,
                    "query_digest": digest,
                    "last_key": [last_score, last_doc_id],
                },
                self._secret,
            )

        return Page(
            hits=tuple(SearchHit(doc_id, score) for _, doc_id, score in page_items),
            next_cursor=next_cursor,
            index_version=snapshot.version,
        )
