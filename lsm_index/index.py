"""LSM-style text index: immutable segments + deletion bitmaps + MVCC snapshots.

不变式
------
1. 段（Segment）一经写入即不可变；新版本写入新段，旧段记录原样保留。
2. 每段维护删除位图 live（int，1=可见）。新版本遮蔽旧版本、墓碑遮蔽活文档，
   都通过重算位图实现；快照在打开时刻冻结自己的位图副本（int copy-on-write），
   之后后台如何重算都不影响旧读者。
3. 快照（Snapshot）持有打开时刻的段列表与位图景，旧读者永远查旧段。
4. 后台合并在锁外构建新段，发布时原子替换；合并期间的 flush 追加到尾部，
   新值不会丢（新段位置更新，合并结果里的旧值会被重新置为遮蔽）。
5. 墓碑只有在“仍存在比它更旧、且它需要遮蔽的段”时才带入合并结果；
   更旧段被覆盖到位之前不丢墓碑。
"""

from __future__ import annotations

import threading
from collections.abc import Callable

from .segment import Segment
from .tokenizer import tokenize

MergeHook = Callable[[str, dict], None]


class Snapshot:
    """某一时刻的只读视图：冻结段列表与每段删除位图。"""

    def __init__(self, index: "LsmTextIndex"):
        self._index = index
        with index._lock:
            # (段, 打开时刻的 live 位图)；int 不可变，后续重算不会改动旧读者所见
            self._views: list[tuple[Segment, int]] = [(s, s.live) for s in index._segments]
            index._pins += 1
        self._closed = False

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            self._index._unpin()

    def __enter__(self) -> "Snapshot":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    @property
    def segments(self) -> list[tuple[int, int, int]]:
        """(seg_id, 记录数, 可见数)，oldest -> newest。"""
        return [(s.seg_id, len(s.records), live.bit_count()) for s, live in self._views]

    # ---------- 查询 ----------

    def _term_visible_ids(self, term: str) -> set[str]:
        result: set[str] = set()
        for seg, live in reversed(self._views):  # newest first
            bits = seg.term_doc_bits(term) & live
            while bits:
                low = bits & -bits
                result.add(seg.records[low.bit_length() - 1].doc_id)
                bits ^= low
        return result

    def term_doc_ids(self, term: str) -> list[str]:
        return sorted(self._term_visible_ids(term))

    def search(self, query: str) -> list[str]:
        """所有 term 同时命中（AND），只返回当前可见文档，结果按 doc_id 排序。"""
        terms = tokenize(query)
        if not terms:
            return []
        hits = self._term_visible_ids(terms[0])
        for term in terms[1:]:
            hits &= self._term_visible_ids(term)
            if not hits:
                break
        return sorted(hits)

    def get_doc(self, doc_id: str) -> str | None:
        """取文档当前正文；被删除或不存在返回 None（沿段从新到旧找可见版本）。"""
        for seg, live in reversed(self._views):
            rec = seg.get(doc_id)
            if rec is not None:
                bit = seg.bit_of(doc_id)
                if (live >> bit) & 1 and not rec.deleted:
                    return rec.text
                return None  # 更新的段已遮蔽，旧段无需再看
        return None

    def visible_doc_ids(self) -> list[str]:
        seen: set[str] = set()
        for seg, live in reversed(self._views):
            bits = live
            while bits:
                low = bits & -bits
                seen.add(seg.records[low.bit_length() - 1].doc_id)
                bits ^= low
        return sorted(seen)

    def count_visible(self) -> int:
        """可见文档数：每篇文档即使存在于多段也只计一次。"""
        return len(self.visible_doc_ids())

    def stats(self, query: str | None = None) -> dict:
        """统计只算可见文档。

        对每个 term 给出 doc_freq（含该词的可见文档数）与 term_freq（出现次数）；
        给查询时额外返回 AND 命中数与命中文档。
        """
        terms = tokenize(query) if query else []
        per_term: dict[str, dict] = {}
        for term in terms:
            df = 0
            tf = 0
            for seg, live in self._views:
                df += (seg.term_doc_bits(term) & live).bit_count()
                tf += seg.term_freq(term, live)
            per_term[term] = {"doc_freq": df, "term_freq": tf}
        matched = self.search(query) if query else []
        return {
            "visible_docs": self.count_visible(),
            "terms": per_term,
            "match_count": len(matched),
            "matching_docs": matched,
        }


class LsmTextIndex:
    def __init__(self, merge_hook: MergeHook | None = None):
        self._lock = threading.RLock()
        self._segments: list[Segment] = []  # oldest -> newest
        self._mem: dict[str, object] = {}  # 待 flush 的最新写 (DocRecord)
        self._next_seg_id = 1
        self._next_version = 1
        self._pins = 0
        self._retired: list[Segment] = []  # 被合并替换、为旧读者保留的旧段
        self._merge_lock = threading.Lock()
        self.merge_hook = merge_hook

    # ---------- 快照 ----------

    def open_snapshot(self) -> Snapshot:
        return Snapshot(self)

    def _unpin(self) -> None:
        with self._lock:
            self._pins -= 1
            if self._pins == 0:
                # 无旧读者后，被替换的旧段才允许丢弃
                self._retired.clear()

    # ---------- 写入 ----------

    def _alloc_version(self) -> int:
        v = self._next_version
        self._next_version += 1
        return v

    def upsert(self, doc_id: str, text: str, version: int | None = None) -> int:
        """写入/更新文档，返回逻辑版本。"""
        from .segment import DocRecord

        with self._lock:
            v = self._alloc_version() if version is None else version
            rec = DocRecord(doc_id, v, False, text)
            old = self._mem.get(doc_id)
            if old is None or v >= old.version:
                self._mem[doc_id] = rec
            return v

    def delete(self, doc_id: str, version: int | None = None) -> int:
        """写入墓碑。"""
        from .segment import DocRecord

        with self._lock:
            v = self._alloc_version() if version is None else version
            rec = DocRecord(doc_id, v, True, "")
            old = self._mem.get(doc_id)
            if old is None or v >= old.version:
                self._mem[doc_id] = rec
            return v

    def flush(self) -> Segment | None:
        """把内存写冻结成一个新的不可变段，并重算各段遮蔽位图。"""
        with self._lock:
            if not self._mem:
                return None
            records = list(self._mem.values())  # type: ignore[arg-type]
            seg = Segment(self._next_seg_id, records)
            self._next_seg_id += 1
            self._segments.append(seg)
            self._mem.clear()
            self._recompute_live_locked()
            return seg

    def _recompute_live_locked(self) -> None:
        """每个 doc_id 只有最新段中的那一份可见（无论活文档还是墓碑）。"""
        seen: set[str] = set()
        for seg in reversed(self._segments):
            live = 0
            for bit, rec in enumerate(seg.records):
                if rec.doc_id not in seen:
                    seen.add(rec.doc_id)
                    if not rec.deleted:
                        live |= 1 << bit
            seg.live = live

    # ---------- 合并 ----------

    def active_segments(self) -> list[tuple[int, int, int]]:
        with self._lock:
            return [(s.seg_id, len(s.records), s.live_count()) for s in self._segments]

    def retired_count(self) -> int:
        with self._lock:
            return len(self._retired)

    def merge(self, seg_ids: list[int] | None = None, hook: MergeHook | None = None) -> Segment | None:
        """合并指定段（默认全部）。在锁外构建、锁内原子发布。

        - 合并期间允许并发 upsert/flush；新值不会丢。
        - 墓碑按需保留：仍有更旧段需要它遮蔽时才带入新段。
        """
        hook = hook or self.merge_hook
        # merge_lock 保证两段合并互不穿插；_lock 只在取计划/发布时短暂持有
        with self._merge_lock:
            with self._lock:
                if seg_ids is None:
                    chosen = list(self._segments)
                else:
                    wanted = set(seg_ids)
                    chosen = [s for s in self._segments if s.seg_id in wanted]
                    if len(chosen) != len(wanted):
                        raise ValueError("seg_ids 包含不存在的段")
                if len(chosen) < 2:
                    return chosen[0] if chosen else None
                positions = [self._segments.index(s) for s in chosen]
                if positions != list(range(positions[0], positions[0] + len(positions))):
                    raise ValueError("只允许合并位置连续的段")
                start = positions[0]
                span = len(chosen)
                plan = [(s.seg_id, list(s.records)) for s in chosen]
                # 合并范围外：更旧段中每个 doc 的最大版本（决定墓碑是否必须保留）
                old_max: dict[str, int] = {}
                for seg in self._segments[:start]:
                    for rec in seg.records:
                        if rec.version > old_max.get(rec.doc_id, -1):
                            old_max[rec.doc_id] = rec.version
                # 合并范围外：更新的段（flush 只能追加在尾部）持有的 doc
                newer_docs: set[str] = set()
                for seg in self._segments[start + span:]:
                    for rec in seg.records:
                        newer_docs.add(rec.doc_id)

            if hook:
                hook("before_build", {"seg_ids": [sid for sid, _ in plan]})

            carried = self._build_merged_records(plan, old_max, newer_docs)

            if hook:
                hook("before_publish", {"carried": len(carried)})

            return self._publish_merge(plan, carried)

    @staticmethod
    def _build_merged_records(
        plan: list[tuple[int, list]],
        old_max: dict[str, int],
        newer_docs: set[str],
    ) -> list:
        # 取计划范围内每个 doc 的最新版本（version 大的胜；并列时更靠右的段更新）
        winner: dict[str, tuple[int, int, object]] = {}
        for rank, (_sid, records) in enumerate(plan):
            for rec in records:
                cur = winner.get(rec.doc_id)
                if cur is None or (rec.version, rank) > (cur[0], cur[1]):
                    winner[rec.doc_id] = (rec.version, rank, rec)

        carried = []
        for doc_id, (version, _rank, rec) in winner.items():
            if doc_id in newer_docs:
                # 合并期间该文档又被更新：新段会遮蔽合并结果，直接不带，绝不覆盖新值
                continue
            if rec.deleted:
                # 墓碑未覆盖所有更旧段前不可丢：
                # 仅当范围外确实存在版本更旧的记录时才保留
                if doc_id in old_max and old_max[doc_id] < version:
                    carried.append(rec)
            else:
                carried.append(rec)
        carried.sort(key=lambda r: r.doc_id)
        return carried

    def _publish_merge(self, plan: list[tuple[int, list]], carried: list) -> Segment | None:
        with self._lock:
            plan_ids = [sid for sid, _ in plan]
            id_set = set(plan_ids)
            positions = [i for i, s in enumerate(self._segments) if s.seg_id in id_set]
            # flush 只可能追加到尾部：计划段必须仍连续存在且顺序不变
            if positions != list(range(positions[0], positions[0] + len(plan_ids))) or \
                    [self._segments[i].seg_id for i in positions] != plan_ids:
                raise RuntimeError("合并发布时发现段布局变化，中止（不会丢数据）")
            start = positions[0]
            old_segments = self._segments[start:start + len(plan_ids)]

            if carried:
                merged = Segment(self._next_seg_id, carried)
                self._next_seg_id += 1
                self._segments[start:start + len(plan_ids)] = [merged]
            else:
                merged = None
                self._segments[start:start + len(plan_ids)] = []

            self._recompute_live_locked()
            # 为旧读者保留旧段；所有快照释放后由 _unpin 清空
            self._retired.extend(old_segments)
            if self._pins == 0:
                self._retired.clear()
            return merged

    # ---------- 后台合并（演示/测试用确定性封装） ----------

    def merge_background(
        self,
        seg_ids: list[int] | None = None,
        during_build: Callable[["LsmTextIndex"], None] | None = None,
    ) -> "threading.Thread":
        """在后台线程执行合并。

        during_build(index) 会在锁外构建阶段被调用一次（在调用线程上），
        可安全地执行 upsert/flush，用于确定性地演示“合并中更新不丢新值”。
        """
        event = threading.Event()

        def hook(stage: str, info: dict) -> None:
            if stage == "before_build":
                event.set()

        def worker() -> None:
            self.merge(seg_ids, hook=hook)

        thread = threading.Thread(target=worker, name="segment-merger", daemon=True)
        thread.start()
        event.wait()
        if during_build is not None:
            during_build(self)
        return thread
