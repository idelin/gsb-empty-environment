"""固定索引版本 + 排序键（keyset）分页引擎。

设计要点：

- 每次 ``update`` 生成一个新的索引版本号；查询与游标都绑定到某个版本。
- 排序键为 ``(-score, doc_id)``：评分降序，同分文档按 ID 升序，构成全序，
  因此分页边界唯一，不重复、不漏项。
- 游标是不透明字符串，内部记录 ``(索引版本, 查询摘要, 最后一条的排序键)``。
  页大小不写入游标，翻页时按排序键续读，因此改变页大小不会重读已读文档。
- 查询摘要 = 规范化后的查询词元集合的 SHA-256；换查询复用游标会被拒绝。
- 更新索引时可选择保留旧快照（旧游标继续可用）或明确失效（旧游标报错）。
"""

from __future__ import annotations

import base64
import hashlib
import json
from collections import Counter
from dataclasses import dataclass
from typing import Iterable

from .tokenizer import tokenize

__all__ = [
    "CursorError",
    "CursorQueryMismatch",
    "CursorInvalidated",
    "SearchHit",
    "Page",
    "SearchEngine",
]

_SORT_SPEC = "score_desc,id_asc"


class CursorError(Exception):
    """游标相关错误的基类。"""


class CursorQueryMismatch(CursorError):
    """游标绑定的查询摘要与当前查询不一致，拒绝复用。"""


class CursorInvalidated(CursorError):
    """游标绑定的索引版本已失效（快照未保留或被显式清理）。"""


@dataclass(frozen=True)
class SearchHit:
    doc_id: str
    score: int
    text: str


@dataclass(frozen=True)
class Page:
    items: tuple[SearchHit, ...]
    next_cursor: str | None  # 还有后续结果时为不透明游标，否则为 None
    index_version: int


def _query_digest(terms: tuple[str, ...]) -> str:
    payload = {"terms": sorted(terms), "sort": _SORT_SPEC}
    raw = json.dumps(payload, ensure_ascii=False, sort_keys=True).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


def _encode_cursor(version: int, digest: str, score: int, doc_id: str) -> str:
    payload = {"v": version, "q": digest, "s": score, "d": doc_id}
    raw = json.dumps(payload, ensure_ascii=False, sort_keys=True).encode("utf-8")
    return base64.urlsafe_b64encode(raw).decode("ascii")


def _decode_cursor(cursor: str) -> dict:
    try:
        raw = base64.urlsafe_b64decode(cursor.encode("ascii"))
        payload = json.loads(raw.decode("utf-8"))
        return {
            "v": int(payload["v"]),
            "q": str(payload["q"]),
            "s": int(payload["s"]),
            "d": str(payload["d"]),
        }
    except Exception as exc:  # noqa: BLE001 - 统一归为游标格式错误
        raise CursorError(f"无法解析的游标: {exc}") from exc


class _Snapshot:
    """某一版本的不可变索引快照。"""

    def __init__(self, version: int, documents: Iterable[tuple[str, str]]):
        self.version = version
        self.docs: dict[str, tuple[str, Counter]] = {}
        for doc_id, text in documents:
            if doc_id in self.docs:
                raise ValueError(f"重复的文档 ID: {doc_id!r}")
            self.docs[doc_id] = (text, Counter(tokenize(text)))

    def match(self, terms: tuple[str, ...]) -> list[tuple[tuple[int, str], SearchHit]]:
        """返回 [(排序键, 命中)]，排序键为 (-score, doc_id)，保证全序。"""
        hits = []
        for doc_id, (text, counts) in self.docs.items():
            if all(counts.get(t, 0) > 0 for t in terms):  # AND 语义
                score = sum(counts[t] for t in terms)
                hits.append(((-score, doc_id), SearchHit(doc_id, score, text)))
        hits.sort(key=lambda pair: pair[0])
        return hits


class SearchEngine:
    """管理索引版本、快照与基于排序键的分页查询。"""

    def __init__(self) -> None:
        self._version = 0
        self._current = _Snapshot(0, [])
        self._snapshots: dict[int, _Snapshot] = {}

    @property
    def version(self) -> int:
        return self._version

    def update(
        self,
        documents: Iterable[tuple[str, str]],
        *,
        invalidate_previous: bool = False,
    ) -> int:
        """重建索引并递增版本号。

        ``invalidate_previous=False``（默认）：保留旧版本快照，旧游标继续可用；
        ``invalidate_previous=True``：明确失效所有旧版本，旧游标再次使用将报错。
        """
        if invalidate_previous:
            self._snapshots.clear()
        elif self._version > 0:
            self._snapshots[self._version] = self._current
        self._version += 1
        self._current = _Snapshot(self._version, documents)
        return self._version

    def invalidate(self, version: int | None = None) -> None:
        """显式失效旧快照；``version=None`` 表示失效全部旧版本。"""
        if version is None:
            self._snapshots.clear()
        else:
            self._snapshots.pop(version, None)

    def search(
        self,
        query_text: str,
        *,
        page_size: int = 10,
        cursor: str | None = None,
    ) -> Page:
        if page_size < 1:
            raise ValueError("page_size 必须 >= 1")
        terms = tuple(sorted(set(tokenize(query_text))))
        digest = _query_digest(terms)

        last_key: tuple[int, str] | None = None
        if cursor is None:
            snapshot = self._current
        else:
            payload = _decode_cursor(cursor)
            if payload["q"] != digest:
                raise CursorQueryMismatch(
                    "游标绑定的查询摘要与当前查询不一致，拒绝复用"
                )
            version = payload["v"]
            if version == self._version:
                snapshot = self._current
            elif version in self._snapshots:
                snapshot = self._snapshots[version]
            else:
                raise CursorInvalidated(
                    f"游标绑定的索引版本 v{version} 已失效，请重新发起查询"
                )
            last_key = (-payload["s"], payload["d"])

        hits = snapshot.match(terms)
        if last_key is not None:
            hits = [pair for pair in hits if pair[0] > last_key]

        page_hits = hits[:page_size]
        remaining = hits[page_size:]
        items = tuple(hit for _, hit in page_hits)

        next_cursor = None
        if page_hits and remaining:
            (neg_score, doc_id), _ = page_hits[-1]
            next_cursor = _encode_cursor(snapshot.version, digest, -neg_score, doc_id)
        return Page(items=items, next_cursor=next_cursor, index_version=snapshot.version)
