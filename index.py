"""不可变段 + 删除位图（墓碑）索引。

核心设计：

- 段（Segment）不可变：每次 put/delete 生成一个新段，追加到段列表头部；
  段一旦创建永不修改，因此读快照无需加锁。
- 版本遮蔽：每个文档的每次写入分配单调递增的版本号；同一 docid 的旧版本
  条目在读取时被新版本遮蔽。
- 删除位图（墓碑）：delete 在段中记录 docid -> 删除时的版本号，
  所有版本号 <= 墓碑版本号的条目对该快照不可见；之后重新 put 的更高版本
  不受影响。
- 后台合并：合并线程把若干老段合并成一个新段，先在锁外完成计算，
  再加锁做原子交换。合并期间的新写入落在更新的段里，不会被合并丢失；
  旧快照持有旧段引用，仍可查询。
- 墓碑保留：合并时被墓碑覆盖的条目直接丢弃；但墓碑本身只有在
  “所有未参与合并的段中都不存在被它覆盖的条目”时才能丢弃，
  否则必须随合并结果保留。
- 统计：只统计当前快照中可见（未被删除、未被遮蔽）的文档。
"""

from __future__ import annotations

import threading

from tokenizer import tokenize


class Segment:
    """不可变段：文档条目 + 删除位图（墓碑）+ 倒排表。"""

    __slots__ = ("seg_id", "docs", "deleted", "postings")

    def __init__(self, seg_id: int, docs: dict, deleted: dict):
        self.seg_id = seg_id
        self.docs = dict(docs)        # docid -> (version, text)
        self.deleted = dict(deleted)  # docid -> 墓碑版本号
        postings: dict[str, list[str]] = {}
        for docid, (_version, text) in self.docs.items():
            for token in set(tokenize(text)):
                postings.setdefault(token, []).append(docid)
        for docids in postings.values():
            docids.sort()
        self.postings = postings  # token -> 有序 [docid]

    def __repr__(self) -> str:
        return (
            f"Segment(id={self.seg_id}, docs={len(self.docs)}, "
            f"tombstones={len(self.deleted)})"
        )


class Snapshot:
    """读快照：某一时刻段列表的只读视图。段不可变，故查询无需加锁。"""

    def __init__(self, segments):
        self._segments = tuple(segments)  # 新段在前

    @property
    def segments(self) -> tuple:
        return self._segments

    def _visible(self) -> dict:
        """可见性判定：未被墓碑覆盖、且未被更新版本遮蔽的最高版本条目。"""
        best: dict[str, int] = {}   # docid -> 快照内最高版本号
        tomb: dict[str, int] = {}   # docid -> 快照内最高墓碑版本号
        for seg in self._segments:
            for docid, (version, _text) in seg.docs.items():
                if version > best.get(docid, 0):
                    best[docid] = version
            for docid, version in seg.deleted.items():
                if version > tomb.get(docid, 0):
                    tomb[docid] = version
        visible: dict[str, tuple] = {}
        for seg in self._segments:
            for docid, (version, text) in seg.docs.items():
                if version == best[docid] and version > tomb.get(docid, 0):
                    visible[docid] = (version, text)
        return visible

    def get(self, docid: str):
        entry = self._visible().get(docid)
        return entry[1] if entry is not None else None

    def search(self, query: str) -> list[str]:
        """查询：同一分词器切词，多词元取交集（AND），只返回可见文档。"""
        tokens = tokenize(query)
        if not tokens:
            return []
        visible = self._visible()
        result: set[str] | None = None
        for token in tokens:
            hits: set[str] = set()
            for seg in self._segments:
                for docid in seg.postings.get(token, ()):
                    entry = visible.get(docid)
                    # 命中必须来自可见版本所在的段，被遮蔽的旧版本不算
                    if entry is not None and seg.docs[docid][0] == entry[0]:
                        hits.add(docid)
            result = hits if result is None else (result & hits)
        return sorted(result or ())

    def stats(self) -> dict:
        """统计：只计算可见文档。"""
        visible = self._visible()
        return {
            "visible_docs": len(visible),
            "segments": len(self._segments),
            "segment_details": [
                {
                    "seg_id": seg.seg_id,
                    "docs": len(seg.docs),
                    "tombstones": len(seg.deleted),
                }
                for seg in self._segments
            ],
        }


class Index:
    """索引本体：段列表 + 版本分配 + 后台合并。"""

    def __init__(self, merge_threshold: int = 8, merge_hook=None):
        self._lock = threading.RLock()
        self._segments: list[Segment] = []  # 新段在前
        self._next_seg_id = 0
        self._versions: dict[str, int] = {}  # docid -> 已分配的最新版本号
        self._merge_threshold = merge_threshold
        self._merge_hook = merge_hook  # 测试钩子：合并计算完成后、交换前回调
        self._stop = threading.Event()
        self._merger: threading.Thread | None = None

    # ---------------- 写入 ----------------

    def _append_segment(self, docs: dict, deleted: dict) -> Segment:
        seg = Segment(self._next_seg_id, docs, deleted)
        self._next_seg_id += 1
        self._segments.insert(0, seg)
        return seg

    def put(self, docid: str, text: str) -> int:
        """写入/更新文档：分配新版本号，生成只含该条目的新段。"""
        with self._lock:
            version = self._versions.get(docid, 0) + 1
            self._versions[docid] = version
            self._append_segment({docid: (version, text)}, {})
            return version

    def delete(self, docid: str) -> bool:
        """删除文档：写入一条墓碑（docid -> 当前最新版本号）。"""
        with self._lock:
            version = self._versions.get(docid, 0)
            if version == 0:
                return False
            self._append_segment({}, {docid: version})
            return True

    # ---------------- 读取 ----------------

    def snapshot(self) -> Snapshot:
        with self._lock:
            return Snapshot(list(self._segments))

    def get(self, docid: str):
        return self.snapshot().get(docid)

    def search(self, query: str) -> list[str]:
        return self.snapshot().search(query)

    def stats(self) -> dict:
        return self.snapshot().stats()

    @property
    def segment_count(self) -> int:
        with self._lock:
            return len(self._segments)

    # ---------------- 合并 ----------------

    @staticmethod
    def _merge_segments(to_merge: list[Segment], others: list[Segment]):
        """把 to_merge 中的段合并为 (docs, deleted) 两部分数据。

        - 同一 docid 只保留合并集合内的最高版本（低版本被遮蔽，丢弃）。
        - 被合并集合内墓碑覆盖的条目直接丢弃。
        - 墓碑保留判断：只要某个未参与合并的段仍含有版本号 <= 墓碑版本号
          的条目，墓碑就必须保留，否则可以安全丢弃。
        """
        docs: dict[str, tuple] = {}
        deleted: dict[str, int] = {}
        for seg in to_merge:
            for docid, (version, text) in seg.docs.items():
                if docid not in docs or version > docs[docid][0]:
                    docs[docid] = (version, text)
            for docid, version in seg.deleted.items():
                if version > deleted.get(docid, 0):
                    deleted[docid] = version
        for docid, version in deleted.items():
            if docid in docs and docs[docid][0] <= version:
                del docs[docid]
        kept: dict[str, int] = {}
        for docid, version in deleted.items():
            for seg in others:
                entry = seg.docs.get(docid)
                if entry is not None and entry[0] <= version:
                    kept[docid] = version
                    break
        return docs, kept

    @staticmethod
    def _find_span(segments: list[Segment], span: list[Segment]):
        """按对象身份在段列表中定位连续子序列，返回起始下标或 None。"""
        n = len(span)
        for i in range(len(segments) - n + 1):
            if all(segments[i + j] is span[j] for j in range(n)):
                return i
        return None

    def _merge_range(self, to_merge: list[Segment], others: list[Segment]):
        """锁外计算合并结果，锁内做原子交换；冲突则放弃返回 None。"""
        docs, deleted = self._merge_segments(to_merge, others)
        if self._merge_hook is not None:
            self._merge_hook()
        with self._lock:
            pos = self._find_span(self._segments, to_merge)
            if pos is None:
                return None  # 目标段已被并发合并改变，放弃本次合并
            merged = Segment(self._next_seg_id, docs, deleted)
            self._next_seg_id += 1
            self._segments = (
                self._segments[:pos] + [merged] + self._segments[pos + len(to_merge):]
            )
            return merged

    def merge_oldest(self, k: int = 2):
        """合并最老的 k 个段（后台合并策略使用）。"""
        with self._lock:
            if len(self._segments) < 2:
                return None
            k = max(2, min(k, len(self._segments)))
            to_merge = list(self._segments[-k:])
            others = list(self._segments[:-k])
        return self._merge_range(to_merge, others)

    def merge_newest(self, k: int = 2):
        """合并最新的 k 个段（用于演示/测试墓碑保留）。"""
        with self._lock:
            if len(self._segments) < 2:
                return None
            k = max(2, min(k, len(self._segments)))
            to_merge = list(self._segments[:k])
            others = list(self._segments[k:])
        return self._merge_range(to_merge, others)

    def merge_all(self):
        """把当前所有段合并成一个段。"""
        with self._lock:
            if len(self._segments) < 2:
                return None
            to_merge = list(self._segments)
        return self._merge_range(to_merge, [])

    # ---------------- 后台合并线程 ----------------

    def start_background_merge(self, interval: float = 0.05) -> None:
        """后台合并：段数超过阈值时反复合并最老的两个段。"""
        if self._merger is not None:
            return

        def loop():
            while not self._stop.is_set():
                if self.segment_count > self._merge_threshold:
                    self.merge_oldest(k=2)
                else:
                    self._stop.wait(interval)

        self._merger = threading.Thread(
            target=loop, daemon=True, name="segment-merger"
        )
        self._merger.start()

    def stop_background_merge(self) -> None:
        self._stop.set()
        if self._merger is not None:
            self._merger.join(timeout=5)
            self._merger = None

    def __enter__(self) -> "Index":
        return self

    def __exit__(self, *exc) -> None:
        self.stop_background_merge()
