"""会话:增量检测、缺口处理、局部重分段、补丁事件与导出登记。"""
from __future__ import annotations

from .audio import MultiTrackStore
from .core import DetectorConfig, Hysteresis, window_rms
from .events import (SegmentCreated, SegmentDiscarded, SegmentMerged,
                     SegmentRevised, SegmentSplit)
from .segments import build_segments
from .streaming import FlagFinalizer


class Exporter:
    """导出登记处:记录已导出片段的版本,应用修订事件保持可追溯。"""

    def __init__(self):
        self.records = {}
        self.history = []

    def is_active(self, segment_id):
        rec = self.records.get(segment_id)
        return rec is not None and rec["status"] == "active"

    def version_of(self, segment_id):
        rec = self.records.get(segment_id)
        return rec["version"] if rec else None

    def export(self, segment):
        if self.is_active(segment.id):
            raise ValueError(f"segment {segment.id} already exported")
        rec = {"id": segment.id, "start": segment.start, "end": segment.end,
               "version": 1, "status": "active", "merged_into": None}
        self.records[segment.id] = rec
        self.history.append(("export", segment.id, 1, segment.start, segment.end))
        return rec

    def apply(self, event):
        if event.kind == "revised":
            rec = self.records.get(event.segment_id)
            if rec and rec["status"] == "active":
                rec["version"] = event.version
                rec["start"], rec["end"] = event.new_start, event.new_end
                self.history.append(
                    ("revise", event.segment_id, event.version,
                     event.new_start, event.new_end))
        elif event.kind == "merged":
            for aid in event.absorbed_ids:
                rec = self.records.get(aid)
                if rec and rec["status"] == "active":
                    rec["status"] = "merged"
                    rec["merged_into"] = event.surviving_id
                    self.history.append(("absorb", aid, event.surviving_id))
            rec = self.records.get(event.surviving_id)
            if rec and rec["status"] == "active":
                rec["version"] += 1
                rec["start"], rec["end"] = event.start, event.end
                self.history.append(
                    ("merge-grow", event.surviving_id, rec["version"],
                     event.start, event.end))
        elif event.kind == "split":
            rec = self.records.get(event.source_id)
            if rec and rec["status"] == "active":
                first = event.ranges[0]
                rec["version"] += 1
                rec["start"], rec["end"] = first[1], first[2]
                self.history.append(
                    ("split-shrink", event.source_id, rec["version"],
                     first[1], first[2]))
            ranges = {rid: (s, e) for rid, s, e in event.ranges}
            for pid in event.created_ids:
                rec = self.records.get(pid)
                if rec and rec["status"] != "active":
                    # 拆分产物的稳定 ID 曾导出过:复活并升版,保持可追溯。
                    rec["status"] = "active"
                    rec["merged_into"] = None
                    rec["version"] += 1
                    rec["start"], rec["end"] = ranges[pid]
                    self.history.append(
                        ("revive", pid, rec["version"],
                         rec["start"], rec["end"]))
        elif event.kind == "discarded":
            rec = self.records.get(event.segment_id)
            if rec and rec["status"] == "active":
                rec["status"] = "discarded"
                self.history.append(("discard", event.segment_id))


def diff_segments(old, new, exporter):
    """对比新旧片段列表,产出局部补丁事件(未变化的片段不产生事件)。"""
    events = []
    old_by_id = {s.id: s for s in old}
    new_by_id = {s.id: s for s in new}

    absorbed = {}
    merged_survivors = set()
    for ns in new:
        victims = [o.id for o in old
                   if o.id != ns.id and ns.core_start < o.core_start < ns.core_end]
        if victims:
            merged_survivors.add(ns.id)
            events.append(SegmentMerged(ns.id, tuple(sorted(victims)),
                                        ns.start, ns.end))
            for v in victims:
                absorbed[v] = ns.id

    for os_ in old:
        if os_.id not in new_by_id and os_.id not in absorbed:
            events.append(SegmentDiscarded(os_.id, exporter.is_active(os_.id)))

    split_products = set()
    for os_ in old:
        ns = new_by_id.get(os_.id)
        if ns is None:
            continue
        products = [n for n in new
                    if n.id != os_.id
                    and os_.core_start < n.core_start < os_.core_end]
        if products:
            ranges = tuple([(ns.id, ns.start, ns.end)]
                           + [(p.id, p.start, p.end) for p in products])
            events.append(SegmentSplit(
                os_.id, tuple(p.id for p in products), ranges))
            split_products.update(p.id for p in products)
        elif os_.id not in merged_survivors and \
                (ns.start, ns.end) != (os_.start, os_.end):
            version = None
            if exporter.is_active(ns.id):
                version = exporter.version_of(ns.id) + 1
            events.append(SegmentRevised(ns.id, os_.start, os_.end,
                                         ns.start, ns.end, version))

    for ns in new:
        if ns.id not in old_by_id and ns.id not in split_products:
            events.append(SegmentCreated(ns.id, ns.start, ns.end))
    return events


class Session:
    """检测会话。

    * ``write`` 追加/覆盖音频,``retract`` 把区间置零(已知静音),
      ``erase`` 把区间抹除回缺口(未知);
    * 静音标记(``add_marker``)可强制局部静音/有声,修订后局部重分段;
    * 每次变更只重算受影响窗口起的分类(利用迟滞死区外的同步点),
      并以补丁事件输出片段的合并/拆分/修订。
    """

    def __init__(self, config: DetectorConfig, tracks=("main",)):
        self.config = config
        self.store = MultiTrackStore(tracks, config.sample_rate)
        self.markers = []  # (start, end, "force_silence" | "force_sound")
        self.exporter = Exporter()
        self.events_log = []
        self._rms = {t: {} for t in self.store.tracks}
        self._track_flags = {t: [] for t in self.store.tracks}
        self._flags = []
        self._segments = []
        self._finished = False
        self._dirty_window = 0

    # ---- 音频输入 ----
    def write(self, track, start, samples, overwrite=False):
        self._require_open()
        self.store[track].write(start, samples, overwrite=overwrite)
        self._mark_dirty(start)
        return self._refresh()

    def retract(self, start, end):
        """撤回区间内容,以数字静音(零)填补,变为"已知静音"。"""
        self._require_open()
        for t in self.store.tracks:
            self.store[t].write(start, [0.0] * (end - start), overwrite=True)
        self._mark_dirty(start)
        return self._refresh()

    def erase(self, start, end):
        """抹除区间,使其回到缺口(未知)状态。"""
        self._require_open()
        for t in self.store.tracks:
            self.store[t].erase(start, end)
        self._mark_dirty(start)
        return self._refresh()

    # ---- 静音标记 ----
    def add_marker(self, start, end, kind="force_silence"):
        self._require_open()
        if kind not in ("force_silence", "force_sound"):
            raise ValueError("kind must be 'force_silence' or 'force_sound'")
        self.markers.append((start, end, kind))
        return self._refresh()  # 标记不影响迟滞分类,无需重算 RMS

    def clear_markers(self):
        self._require_open()
        self.markers.clear()
        return self._refresh()

    # ---- 推进与收尾 ----
    def poll(self):
        return self._refresh()

    def finish(self):
        self._require_open()
        self._finished = True
        return self._refresh()

    @property
    def segments(self):
        return list(self._segments)

    @property
    def stalled(self):
        """是否因缺口阻塞:已写入内容的末端超出连续覆盖前缀。"""
        written_end = max((self.store[t].written_end
                           for t in self.store.tracks), default=0)
        return self.store.contiguous_prefix() < written_end

    def export_all(self):
        return [self.exporter.export(s) for s in self._segments
                if not self.exporter.is_active(s.id)]

    # ---- 内部 ----
    def _require_open(self):
        if self._finished:
            raise RuntimeError("session already finished")

    def _mark_dirty(self, start):
        w = start // self.config.window_size
        self._dirty_window = min(self._dirty_window, w)

    def _refresh(self):
        self._refresh_flags()
        finalizer = FlagFinalizer(self.config.min_sound_windows,
                                  self.config.min_silence_windows)
        for f in self._flags:
            finalizer.push(f)
        cores = finalizer.finish() if self._finished else finalizer.committed
        return self._reconcile(cores)

    def _refresh_flags(self):
        cfg = self.config
        n_windows = self.store.contiguous_prefix() // cfg.window_size
        for t in self.store.tracks:
            self._recompute_track(t, self._dirty_window, n_windows)
        flags = []
        for widx in range(n_windows):
            vals = [self._track_flags[t][widx] for t in self.store.tracks]
            flag = all(vals) if cfg.strategy == "all" else any(vals)
            marker = self._marker_at(widx * cfg.window_size,
                                     (widx + 1) * cfg.window_size)
            if marker == "force_silence":
                flag = False
            elif marker == "force_sound":
                flag = True
            flags.append(flag)
        self._flags = flags
        self._dirty_window = n_windows

    def _recompute_track(self, track, dirty, n_windows):
        cfg = self.config
        w = cfg.window_size
        store = self.store[track]
        rms_cache = self._rms[track]
        flags = self._track_flags[track]
        for widx in [k for k in rms_cache if k >= dirty or k >= n_windows]:
            del rms_cache[widx]
        # 同步点:向前找 RMS 落在迟滞死区之外、状态与历史无关的窗口。
        sync = 0
        state = False
        widx = min(dirty, len(flags)) - 1
        while widx >= 0:
            r = self._rms_value(store, rms_cache, widx)
            if r > cfg.open_threshold * (1.0 + cfg.tolerance):
                state, sync = True, widx + 1
                break
            if r < cfg.close_threshold * (1.0 - cfg.tolerance):
                state, sync = False, widx + 1
                break
            widx -= 1
        hyst = Hysteresis(cfg.open_threshold, cfg.close_threshold,
                          cfg.tolerance, state)
        del flags[sync:]
        for widx in range(sync, n_windows):
            flags.append(hyst.classify(self._rms_value(store, rms_cache, widx)))
        del flags[n_windows:]

    def _rms_value(self, store, cache, widx):
        r = cache.get(widx)
        if r is None:
            w = self.config.window_size
            r = window_rms(store.read(widx * w, (widx + 1) * w))
            cache[widx] = r
        return r

    def _marker_at(self, start, end):
        result = None
        for ms, me, kind in self.markers:
            if ms < end and start < me:
                result = kind  # 后添加的标记优先
        return result

    def _reconcile(self, cores):
        cfg = self.config
        total = len(self._flags) * cfg.window_size
        new_segments = build_segments(cores, total, cfg.window_size,
                                      cfg.pre_pad, cfg.post_pad)
        events = diff_segments(self._segments, new_segments, self.exporter)
        self._segments = new_segments
        for e in events:
            self.exporter.apply(e)
        self.events_log.extend(events)
        return events
