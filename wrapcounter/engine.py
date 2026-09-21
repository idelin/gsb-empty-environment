"""增量乱序计数器分析引擎。

不变量：
- 每个来源内部维护按 (event_time, seq[, sample_id]) 的全序采样。
- 任意时刻的当前区间集合都与 reference.recompute_source 完全一致
  （测试中逐步对照）。
- 迟到插入 / 撤回 / 替换只修订相邻差分：先 REVOKE 受影响的旧区间，
  再 UPSERT 新区间；远离插入点的区间保持原 interval_id 与结果不变。
- 所有修订带全局单调 revision_id；区间结果带 config_version 与采样 ID。
"""

from __future__ import annotations

from bisect import bisect_left
from collections.abc import Mapping

from .judge import judge_interval
from .model import (
    DuplicatePolicy,
    IntervalResult,
    Reason,
    Revision,
    RevisionOp,
    Sample,
    SourceConfig,
    SourceTotal,
    Status,
    build_source_total,
)


class CounterAnalyzer:
    def __init__(self) -> None:
        self._configs: dict[str, SourceConfig] = {}
        self._samples: dict[str, dict[str, Sample]] = {}
        self._keys: dict[str, list[tuple[float, int, str]]] = {}
        self._intervals: dict[str, dict[str, IntervalResult]] = {}
        self._revisions: list[Revision] = []
        self._next_revision_id = 1

    # ------------------------------------------------------------------ config

    def register_source(self, config: SourceConfig) -> list[Revision]:
        """注册一个新来源；已存在时请用 update_config。"""
        if config.source in self._configs:
            raise ValueError(f"source {config.source} 已注册，请用 update_config")
        self._configs[config.source] = config
        self._samples[config.source] = {}
        self._keys[config.source] = []
        self._intervals[config.source] = {}
        return []

    def update_config(self, config: SourceConfig) -> list[Revision]:
        """更新来源配置（新版本）。旧版本判定的区间全部作废并按新版本重算。"""
        if config.source not in self._configs:
            self.register_source(config)
            return []
        emitted: list[Revision] = []
        state_intervals = self._intervals[config.source]
        old_version = self._configs[config.source].config_version
        old_ivs = list(state_intervals.values())
        state_intervals.clear()
        self._configs[config.source] = config
        for iv in old_ivs:
            emitted.append(
                self._rev(
                    RevisionOp.REVOKE,
                    config.source,
                    iv,
                    reason=f"配置版本 {old_version} -> {config.config_version}，"
                    f"v{old_version} 判定作废",
                    config_version=old_version,
                )
            )

        ordered = self._ordered(config.source)
        prev: Sample | None = None
        for curr in ordered:
            iv = judge_interval(prev, curr, config)
            state_intervals[iv.interval_id] = iv
            emitted.append(
                self._rev(
                    RevisionOp.UPSERT,
                    config.source,
                    iv,
                    reason="按新配置版本重算",
                )
            )
            prev = curr
        return emitted

    # ------------------------------------------------------------------ samples

    def upsert_sample(
        self,
        sample: Sample,
        duplicate_policy: DuplicatePolicy = DuplicatePolicy.ERROR,
    ) -> list[Revision]:
        """插入或迟到补样。返回本次产生的修订序列（仅相邻差分）。"""
        config = self._require_config(sample.source)
        bucket = self._samples[sample.source]
        keys = self._keys[sample.source]
        state_intervals = self._intervals[sample.source]

        key = (sample.event_time, sample.seq, sample.sample_id)
        dup_key = (sample.event_time, sample.seq)
        pos = bisect_left(keys, (sample.event_time, sample.seq, ""))
        if pos < len(keys) and (keys[pos][0], keys[pos][1]) == dup_key:
            old = bucket[keys[pos][2]]
            if old.sample_id == sample.sample_id:
                return self._replace_in_place(old, sample)
            if duplicate_policy is DuplicatePolicy.IGNORE:
                return []
            if duplicate_policy is DuplicatePolicy.ERROR:
                raise ValueError(
                    f"source={sample.source} 在 t={sample.event_time}, seq={sample.seq} "
                    f"已存在采样 {old.sample_id}（新采样 {sample.sample_id}）"
                )
            return self._replace_in_place(old, sample)

        prev_s = bucket[keys[pos - 1][2]] if pos > 0 else None
        next_s = bucket[keys[pos][2]] if pos < len(keys) else None

        emitted: list[Revision] = []
        if prev_s is not None and next_s is not None:
            old_iv = state_intervals.pop(f"{sample.source}:{prev_s.sample_id}->{next_s.sample_id}")
            emitted.append(
                self._rev(
                    RevisionOp.REVOKE,
                    sample.source,
                    old_iv,
                    reason="迟到插入：旧相邻区间被中间采样拆分，结果作废",
                    sample_id=sample.sample_id,
                )
            )
        elif prev_s is None and next_s is not None:
            old_anchor = state_intervals.pop(f"{sample.source}:ROOT->{next_s.sample_id}")
            emitted.append(
                self._rev(
                    RevisionOp.REVOKE,
                    sample.source,
                    old_anchor,
                    reason="更早采样插入：旧首采样锚点作废",
                    sample_id=sample.sample_id,
                )
            )

        keys.insert(pos, key)
        bucket[sample.sample_id] = sample

        emitted.extend(
            self._emit_edge(prev_s, sample, config, state_intervals,
                            split_from=(f"{sample.source}:{prev_s.sample_id}->{next_s.sample_id}"
                                        if prev_s is not None and next_s is not None else None))
        )
        emitted.extend(
            self._emit_edge(sample, next_s, config, state_intervals,
                            split_from=(f"{sample.source}:{prev_s.sample_id}->{next_s.sample_id}"
                                        if prev_s is not None and next_s is not None else None))
        )
        return emitted

    def retract_sample(self, sample_id: str) -> list[Revision]:
        """撤回采样：作废其前后相邻区间；若两侧都存在则补发合并后的新区间。"""
        source = self._locate_source(sample_id)
        config = self._configs[source]
        bucket = self._samples[source]
        keys = self._keys[source]
        state_intervals = self._intervals[source]
        sample = bucket[sample_id]
        pos = bisect_left(keys, (sample.event_time, sample.seq, sample.sample_id))

        prev_s = bucket[keys[pos - 1][2]] if pos > 0 else None
        next_s = bucket[keys[pos + 1][2]] if pos + 1 < len(keys) else None

        emitted: list[Revision] = []
        left_id = (
            f"{source}:{prev_s.sample_id}->{sample.sample_id}" if prev_s is not None
            else f"{source}:ROOT->{sample.sample_id}"
        )
        if left_id in state_intervals:
            emitted.append(
                self._rev(
                    RevisionOp.REVOKE,
                    source,
                    state_intervals.pop(left_id),
                    reason="采样撤回：以其为右端的相邻区间作废",
                    sample_id=sample_id,
                )
            )
        right_id = (
            f"{source}:{sample.sample_id}->{next_s.sample_id}"
            if next_s is not None else None
        )
        if right_id is not None and right_id in state_intervals:
            emitted.append(
                self._rev(
                    RevisionOp.REVOKE,
                    source,
                    state_intervals.pop(right_id),
                    reason="采样撤回：以其为左端的相邻区间作废",
                    sample_id=sample_id,
                )
            )

        del bucket[sample_id]
        del keys[pos]

        if prev_s is not None and next_s is not None:
            emitted.extend(
                self._emit_edge(prev_s, next_s, config, state_intervals,
                                merge_note=True)
            )
        elif prev_s is None and next_s is not None:
            anchor = judge_interval(None, next_s, config)
            state_intervals[anchor.interval_id] = anchor
            emitted.append(
                self._rev(RevisionOp.UPSERT, source, anchor,
                          reason="撤回后新的首采样锚点", sample_id=sample_id)
            )
        return emitted

    # ------------------------------------------------------------------ reads

    def get_interval(self, source: str, interval_id: str) -> IntervalResult:
        return self._intervals[source][interval_id]

    def current_intervals(self, source: str) -> list[IntervalResult]:
        """当前生效区间，按全序对齐（首元素为首采样锚点）。"""
        config = self._require_config(source)
        ordered = self._ordered(source)
        out: list[IntervalResult] = []
        prev: Sample | None = None
        for curr in ordered:
            iv_id = f"{curr.source}:{prev.sample_id}->{curr.sample_id}" if prev is not None else f"{curr.source}:ROOT->{curr.sample_id}"
            out.append(self._intervals[source][iv_id])
            prev = curr
        return out

    def source_total(self, source: str) -> SourceTotal:
        config = self._require_config(source)
        ordered = self._ordered(source)
        intervals = self.current_intervals(source)
        return build_source_total(source, ordered, intervals[1:], config)

    def all_totals(self) -> dict[str, SourceTotal]:
        return {src: self.source_total(src) for src in self._configs}

    def sample(self, sample_id: str) -> Sample:
        return self._samples[self._locate_source(sample_id)][sample_id]

    def revision_history(self) -> tuple[Revision, ...]:
        return tuple(self._revisions)

    @property
    def revision_count(self) -> int:
        return len(self._revisions)

    # ------------------------------------------------------------------ internals

    def _replace_in_place(self, old: Sample, new: Sample) -> list[Revision]:
        """同键覆盖（REPLACE）：等价撤回旧采样后在原位置插入新采样。"""
        emitted = self.retract_sample(old.sample_id)
        emitted.extend(self.upsert_sample(new, DuplicatePolicy.ERROR))
        return emitted

    def _emit_edge(
        self,
        prev_s: Sample | None,
        curr_s: Sample | None,
        config: SourceConfig,
        state_intervals: dict[str, IntervalResult],
        split_from: str | None = None,
        merge_note: bool = False,
        revoked_left: str | None = None,
    ) -> list[Revision]:
        if curr_s is None:
            return []
        iv = judge_interval(prev_s, curr_s, config)
        state_intervals[iv.interval_id] = iv
        if split_from is not None:
            reason, replaced = "迟到插入：拆分后的新相邻区间", split_from
        elif merge_note:
            reason, replaced = "撤回后两侧相邻区间合并重算", revoked_left
        elif prev_s is None:
            reason, replaced = "首采样锚点（无前序差分）", None
        else:
            reason, replaced = "新区间判定", None
        return [
            self._rev(
                RevisionOp.UPSERT,
                curr_s.source,
                iv,
                reason=reason,
                replaced_interval_id=replaced,
                sample_id=curr_s.sample_id,
            )
        ]

    def _rev(
        self,
        op: RevisionOp,
        source: str,
        interval: IntervalResult | None,
        reason: str,
        replaced_interval_id: str | None = None,
        sample_id: str | None = None,
        config_version: int | None = None,
    ) -> Revision:
        revision = Revision(
            revision_id=self._next_revision_id,
            op=op,
            source=source,
            interval=interval,
            interval_id=interval.interval_id if interval is not None else None,
            replaced_interval_id=replaced_interval_id,
            reason=reason,
            config_version=(self._configs[source].config_version
                            if config_version is None else config_version),
            sample_id=sample_id,
        )
        self._next_revision_id += 1
        self._revisions.append(revision)
        return revision

    def _ordered(self, source: str) -> list[Sample]:
        bucket = self._samples[source]
        return [bucket[k[2]] for k in self._keys[source]]

    def _require_config(self, source: str) -> SourceConfig:
        try:
            return self._configs[source]
        except KeyError:
            raise KeyError(f"source {source} 尚未注册配置") from None

    def _locate_source(self, sample_id: str) -> str:
        for source, bucket in self._samples.items():
            if sample_id in bucket:
                return source
        raise KeyError(f"未知采样 {sample_id}")
