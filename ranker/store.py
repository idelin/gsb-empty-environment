"""版本化存储：文档库、统计快照库、规则库。

所有历史版本不可变地保留，旧解释因此始终绑定旧证据复算，
规则/统计更新后旧解释保持历史有效；历史被清除时，
验证器只能判“不可验证”，绝不能判“无效”。
"""

from __future__ import annotations

import hashlib
import json


def canonical_json(obj) -> str:
    return json.dumps(obj, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def content_hash(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


class DocStore:
    """文档库：每个 doc_id 保留全部历史修订；每次变更产生新的语料版本快照。"""

    def __init__(self):
        self._docs: dict[str, list[dict]] = {}
        self._snapshots: dict[int, dict[str, int]] = {0: {}}
        self.corpus_version = 0

    def _commit(self):
        self.corpus_version += 1
        self._snapshots[self.corpus_version] = {
            doc_id: revs[-1]["version"] for doc_id, revs in self._docs.items()
        }

    def add(self, doc_id: str, text: str, meta: dict) -> dict:
        if doc_id in self._docs:
            raise ValueError(f"文档 {doc_id} 已存在")
        rev = {"version": 1, "text": text, "meta": dict(meta), "hash": content_hash(text)}
        self._docs[doc_id] = [rev]
        self._commit()
        return rev

    def update(self, doc_id: str, text: str, meta: dict | None = None) -> dict:
        """正常替换文档：产生新修订版本，旧版本保留、旧解释仍历史有效。"""
        revs = self._docs[doc_id]
        rev = {
            "version": len(revs) + 1,
            "text": text,
            "meta": dict(meta) if meta is not None else dict(revs[-1]["meta"]),
            "hash": content_hash(text),
        }
        revs.append(rev)
        self._commit()
        return rev

    def get(self, doc_id: str, version: int | None = None) -> dict | None:
        revs = self._docs.get(doc_id)
        if not revs:
            return None
        if version is None:
            return revs[-1]
        for rev in revs:
            if rev["version"] == version:
                return rev
        return None

    def snapshot(self, version: int | None = None) -> dict[str, int] | None:
        """返回 {doc_id: doc_version}；版本不存在时返回 None。"""
        if version is None:
            version = self.corpus_version
        snap = self._snapshots.get(version)
        return dict(snap) if snap is not None else None


class StatsStore:
    """语料统计快照：{version, corpus_version, rules_version, N, df, tf_max}，只增不改。"""

    def __init__(self):
        self._versions: dict[int, dict] = {}

    def put(self, stats: dict) -> int:
        version = stats["version"]
        if version in self._versions:
            raise ValueError(f"统计版本 {version} 已存在")
        self._versions[version] = stats
        return version

    def next_version(self) -> int:
        return max(self._versions, default=0) + 1

    def get(self, version: int) -> dict | None:
        return self._versions.get(version)

    def latest(self) -> dict | None:
        if not self._versions:
            return None
        return self._versions[max(self._versions)]

    def drop(self, version: int) -> None:
        """模拟统计证据被清除：此后绑定该版本的解释只能判不可验证。"""
        self._versions.pop(version, None)


class RulesStore:
    """评分规则版本：{version, tokenizer, idf}，只增不改。"""

    def __init__(self):
        self._versions: dict[int, dict] = {}

    def add(self, tokenizer: str, idf: str) -> int:
        version = max(self._versions, default=0) + 1
        self._versions[version] = {"version": version, "tokenizer": tokenizer, "idf": idf}
        return version

    def get(self, version: int) -> dict | None:
        return self._versions.get(version)

    def latest(self) -> dict | None:
        if not self._versions:
            return None
        return self._versions[max(self._versions)]
