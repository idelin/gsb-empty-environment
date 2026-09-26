"""分段倒排索引：提交、文档撤回、段合并、快照游标、短语搜索。

结构：
    Index 由若干不可变 Segment 组成；add_document 先进入写缓冲，
    commit 时冻结为一个新段。delete 只登记墓碑（tombstone），
    读取侧按“视图”（segments 加 已删除集合）过滤，因此删除后
    位置与文档频率同步生效；merge 把所有段物理压实成一个段并
    丢弃已删除文档。snapshot 固定某一时刻的视图，之后的提交、
    删除、合并都不影响该快照；cursor 在视图上按 docid 顺序翻页，
    并给出可恢复的游标位置。
"""
from __future__ import annotations

import bisect
from dataclasses import dataclass

from .phrase import PhraseQuery, assignment_span, filter_minimal, match_field
from .postings import DecodeStats, FieldPostings
from .tokenize import tokenize


class Segment:
    """不可变索引段：文档存储 加 词项到文档到字段的分块位置表。"""

    __slots__ = ("seg_id", "docs", "terms", "term_docids")

    def __init__(self, seg_id):
        self.seg_id = seg_id
        self.docs = {}
        self.terms = {}
        self.term_docids = {}

    @classmethod
    def build(cls, seg_id, documents):
        """documents: 形如 [(docid, {field: text}), ...] 的列表。"""
        seg = cls(seg_id)
        collected = {}
        for docid, fields in documents:
            stored = {}
            for field_name, text in fields.items():
                tokens = tuple(tokenize(text))
                stored[field_name] = tokens
                for token in tokens:
                    per_doc = collected.setdefault(token.term, {}).setdefault(docid, {})
                    per_doc.setdefault(field_name, []).append(token.pos)
            seg.docs[docid] = stored
        for term, per_doc in collected.items():
            seg.terms[term] = {
                docid: {
                    field_name: FieldPostings(term, positions)
                    for field_name, positions in per_field.items()
                }
                for docid, per_field in per_doc.items()
            }
            seg.term_docids[term] = tuple(sorted(per_doc))
        return seg


@dataclass(frozen=True)
class Match:
    """一次短语命中：原文范围 加 位置证据。"""

    docid: object
    field: str
    start_char: int
    end_char: int
    pos_start: int
    pos_end: int
    evidence: tuple
    text: str


class SearchResult:
    def __init__(self, matches, stats):
        self.matches = matches
        self.stats = stats

    def __iter__(self):
        return iter(self.matches)

    def __len__(self):
        return len(self.matches)


def _slice_source(tokens, pos_start, pos_end):
    """按原字符区间拼接命中片段；词间空隙用空格占位。"""
    parts = []
    prev_end = None
    for token in tokens[pos_start : pos_end + 1]:
        if prev_end is not None and token.start > prev_end:
            parts.append(" ")
        parts.append(token.term)
        prev_end = token.end
    return "".join(parts)


class _View:
    """某一时刻的一致性视图：段列表 加 墓碑集合。"""

    def __init__(self, segments, deleted):
        self.segments = tuple(segments)
        self.deleted = frozenset(deleted)

    def live_docids(self):
        out = []
        for seg in self.segments:
            for docid in sorted(seg.docs):
                if docid not in self.deleted:
                    out.append(docid)
        return sorted(out)

    def get_doc(self, docid):
        if docid in self.deleted:
            return None
        for seg in self.segments:
            if docid in seg.docs:
                return seg.docs[docid]
        return None

    def doc_freq(self, term):
        """活文档中的文档频率；删除后立即同步下降。"""
        df = 0
        for seg in self.segments:
            for docid in seg.term_docids.get(term, ()):
                if docid not in self.deleted:
                    df += 1
        return df

    def _candidate_docids(self, query, stats):
        """逐段做槽位文档集合求交；用二分跳跃代替全量扫描。"""
        candidates = []
        for seg in self.segments:
            slot_docid_lists = []
            for slot in query.slots:
                merged = set()
                for alt in slot:
                    for term in alt:
                        merged.update(seg.term_docids.get(term, ()))
                if not merged:
                    slot_docid_lists = None
                    break
                slot_docid_lists.append(sorted(merged))
            if slot_docid_lists is None:
                continue
            slot_docid_lists.sort(key=len)
            base = slot_docid_lists[0]
            rest = slot_docid_lists[1:]
            for docid in base:
                if docid in self.deleted:
                    continue
                ok = True
                for other in rest:
                    stats.doc_skips += 1
                    idx = bisect.bisect_left(other, docid)
                    if idx >= len(other) or other[idx] != docid:
                        ok = False
                        break
                if ok:
                    candidates.append((seg, docid))
        return candidates

    def search(self, query):
        if not isinstance(query, PhraseQuery):
            raise TypeError("expected PhraseQuery")
        stats = DecodeStats()
        matches = []
        for seg, docid in self._candidate_docids(query, stats):
            stored = seg.docs[docid]
            for field_name, tokens in stored.items():
                term_postings = seg.terms

                def get_fp(term, _docid=docid, _field=field_name, _tp=term_postings):
                    per_doc = _tp.get(term)
                    if per_doc is None:
                        return None
                    per_field = per_doc.get(_docid)
                    if per_field is None:
                        return None
                    return per_field.get(_field)

                assignments = filter_minimal(match_field(query, get_fp, stats))
                for hits in assignments:
                    pos_start, pos_end = assignment_span(hits)
                    matches.append(
                        Match(
                            docid=docid,
                            field=field_name,
                            start_char=tokens[pos_start].start,
                            end_char=tokens[pos_end].end,
                            pos_start=pos_start,
                            pos_end=pos_end,
                            evidence=tuple(hits),
                            text=_slice_source(tokens, pos_start, pos_end),
                        )
                    )
        matches.sort(key=lambda m: (str(m.docid), m.field, m.pos_start, m.pos_end))
        return SearchResult(matches, stats)


class Cursor:
    """快照上的文档游标：按 docid 升序翻页，可凭位置恢复。"""

    def __init__(self, view, after=None):
        self._view = view
        self._docids = view.live_docids()
        self._idx = 0
        if after is not None:
            self._idx = bisect.bisect_right(self._docids, after)

    @property
    def position(self):
        """游标位置（最后产出的 docid）；尚未产出时为 None。"""
        if self._idx == 0:
            return None
        return self._docids[self._idx - 1]

    def next_batch(self, n=1):
        batch = []
        while self._idx < len(self._docids) and len(batch) < n:
            docid = self._docids[self._idx]
            self._idx += 1
            batch.append((docid, self._view.get_doc(docid)))
        return batch

    def exhausted(self):
        return self._idx >= len(self._docids)


class Snapshot:
    """某一时刻的只读一致性视图。"""

    def __init__(self, view):
        self._view = view

    def search(self, query):
        return self._view.search(query)

    def doc_freq(self, term):
        return self._view.doc_freq(term)

    def cursor(self, after=None):
        return Cursor(self._view, after=after)

    def live_docids(self):
        return self._view.live_docids()


class Index:
    """分段倒排索引主类。"""

    def __init__(self):
        self._segments = []
        self._deleted = set()
        self._pending = []
        self._known_docids = set()
        self._next_seg_id = 0

    def add_document(self, docid, fields):
        """fields 形如 {field_name: text}；docid 全库唯一。"""
        if docid in self._known_docids and docid not in self._deleted:
            raise ValueError("duplicate docid: %r" % (docid,))
        if not fields:
            raise ValueError("document needs at least one field")
        self._pending.append((docid, dict(fields)))
        self._known_docids.add(docid)
        self._deleted.discard(docid)

    def commit(self):
        if not self._pending:
            return None
        seg = Segment.build(self._next_seg_id, self._pending)
        self._next_seg_id += 1
        self._segments.append(seg)
        self._pending = []
        return seg.seg_id

    def delete(self, docid):
        """登记墓碑；位置与文档频率在读取侧同步失效。"""
        self._deleted.add(docid)

    def merge(self):
        """把所有段压实成一个段，物理丢弃已删除文档。"""
        self.commit()
        kept = []
        for seg in self._segments:
            for docid in sorted(seg.docs):
                if docid not in self._deleted:
                    kept.append((docid, seg.docs[docid]))
        merged = Segment(self._next_seg_id)
        self._next_seg_id += 1
        collected = {}
        for docid, stored in kept:
            merged.docs[docid] = stored
            for field_name, tokens in stored.items():
                for token in tokens:
                    per_doc = collected.setdefault(token.term, {}).setdefault(docid, {})
                    per_doc.setdefault(field_name, []).append(token.pos)
        for term, per_doc in collected.items():
            merged.terms[term] = {
                docid: {
                    field_name: FieldPostings(term, positions)
                    for field_name, positions in per_field.items()
                }
                for docid, per_field in per_doc.items()
            }
            merged.term_docids[term] = tuple(sorted(per_doc))
        self._segments = [merged]
        return merged.seg_id

    def _view(self):
        self.commit()
        return _View(self._segments, self._deleted)

    def snapshot(self):
        return Snapshot(self._view())

    def search(self, query):
        return self._view().search(query)

    def doc_freq(self, term):
        return self._view().doc_freq(term)

    def cursor(self, after=None):
        return Cursor(self._view(), after=after)

    def segment_count(self):
        self.commit()
        return len(self._segments)

