"""回溯可修订的滚动漂移检测器（精确算术）。

设计要点
========
* 时间窗：对目标事件 (source, t)，基线取同源、事件时间在
  ``[t-lookback, t)`` 内的样本；目标事件自身不计入自身基线。
* 异常段：闭区间 ``[start_time, end_time]``，可补录、可撤回。
  一个样本被任意一个有效（未撤回）异常段覆盖时即被排除。
* 统计：剔除已标异常样本后的滚动总体均值/方差，全部用
  :class:`fractions.Fraction` 精确计算；判据是公开、确定性的
  严格 3σ（总体方差）::

      n >= min_baseline 且 (x - mean)^2 > threshold^2 * variance
  边界相等不漂移（严格大于），临界判定不依赖浮点舍入。
* 修订：异常段补录/撤回只重算“实际依赖”发生变化的结果——
  事件时间在 ``[seg_start, seg_end + lookback]`` 内的同源样本；
  被标记区间内的样本本身改为 EXCLUDED。每次修订产生
  :class:`Revision`，串成逐键修订链。
* 证据：原始样本、段墓碑、配置、逐版判定与操作日志都被保留，
  见 README“需要保留的证据”。
"""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from enum import Enum
from fractions import Fraction
from typing import Iterable, Optional


# ---------------------------------------------------------------------------
# 基础类型
# ---------------------------------------------------------------------------


class DriftStatus(str, Enum):
    """单个事件的判定结果。"""

    DRIFT = "DRIFT"
    NORMAL = "NORMAL"
    EXCLUDED = "EXCLUDED"  # 被有效异常段覆盖，不参与基线/不判定
    UNDECIDABLE = "UNDECIDABLE"  # 基线为空（含全部样本被排除）
    INSUFFICIENT = "INSUFFICIENT"  # 基线非空但数量不足 min_baseline


class OperationKind(str, Enum):
    SAMPLE = "sample"
    ADD_SEGMENT = "add_segment"
    WITHDRAW_SEGMENT = "withdraw_segment"


def to_fraction(value: object) -> Fraction:
    """把用户数值转为精确 Fraction；拒绝 float / NaN / inf。"""
    if isinstance(value, float):
        raise TypeError(
            f"不接受 float 输入（会引入二进制舍入），请用 int/str/Decimal/Fraction: {value!r}"
        )
    if isinstance(value, bool):  # bool 是 int 子类，先排除
        raise TypeError(f"不接受 bool 作为数值: {value!r}")
    if isinstance(value, Fraction):
        result = value
    elif isinstance(value, int):
        result = Fraction(value)
    elif isinstance(value, str):
        text = value.strip()
        try:
            result = Fraction(text)
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"无法把字符串解析为精确数值: {value!r}") from exc
    else:
        try:  # decimal.Decimal 等可精确转为 Fraction 的类型
            result = Fraction(value)  # type: ignore[arg-type]
        except Exception as exc:  # noqa: BLE001 - 统一成 ValueError
            raise TypeError(f"不支持的数值类型: {type(value).__name__}") from exc
    if result.denominator == 0 or result != result:  # NaN 检查
        raise ValueError(f"数值必须有限: {value!r}")
    return result


def _check_time(value: object, name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} 必须是 int（事件时间用整数刻度）: {value!r}")
    return value


@dataclass(frozen=True)
class Sample:
    source: str
    seq: int
    time: int
    value: Fraction

    def __post_init__(self) -> None:
        if not isinstance(self.source, str) or not self.source:
            raise ValueError("source 必须是非空字符串")
        if isinstance(self.seq, bool) or not isinstance(self.seq, int) or self.seq < 0:
            raise ValueError("seq 必须是非负整数")
        _check_time(self.time, "time")
        if not isinstance(self.value, Fraction):
            object.__setattr__(self, "value", to_fraction(self.value))


@dataclass(frozen=True)
class SampleKey:
    source: str
    seq: int


@dataclass(frozen=True)
class AnomalySegment:
    """一个异常段标记。同 source，闭区间 [start_time, end_time]。"""

    source: str
    segment_id: str
    start_time: int
    end_time: int
    note: str = ""

    def __post_init__(self) -> None:
        if not isinstance(self.source, str) or not self.source:
            raise ValueError("source 必须是非空字符串")
        if not isinstance(self.segment_id, str) or not self.segment_id:
            raise ValueError("segment_id 必须是非空字符串")
        _check_time(self.start_time, "start_time")
        _check_time(self.end_time, "end_time")
        if self.end_time < self.start_time:
            raise ValueError("end_time 不能早于 start_time")

    def covers_time(self, t: int) -> bool:
        return self.start_time <= t <= self.end_time


@dataclass(frozen=True)
class DriftConfig:
    lookback: int = 5
    min_baseline: int = 1
    threshold: Fraction = Fraction(3, 1)

    def __post_init__(self) -> None:
        if isinstance(self.lookback, bool) or not isinstance(self.lookback, int) or self.lookback <= 0:
            raise ValueError("lookback 必须是正整数")
        if (
            isinstance(self.min_baseline, bool)
            or not isinstance(self.min_baseline, int)
            or self.min_baseline <= 0
        ):
            raise ValueError("min_baseline 必须是正整数")
        if not isinstance(self.threshold, Fraction):
            object.__setattr__(self, "threshold", to_fraction(self.threshold))
        if self.threshold <= 0:
            raise ValueError("threshold 必须为正数")


# ---------------------------------------------------------------------------
# 判定结果 / 修订
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Decision:
    """一个事件在某一版的判定（判定本身与证据都落盘到这里）。"""

    source: str
    seq: int
    time: int
    value: Fraction
    status: DriftStatus
    baseline_n: int
    mean: Optional[Fraction]
    variance: Optional[Fraction]
    included_keys: tuple[SampleKey, ...]
    excluded_keys: tuple[SampleKey, ...]
    excluded_baseline_segments: tuple[tuple[SampleKey, tuple[str, ...]], ...]
    active_segment_ids: tuple[str, ...]
    basis: str
    version: int


@dataclass(frozen=True)
class Change:
    field: str
    before: object
    after: object


@dataclass(frozen=True)
class Revision:
    operation_index: int
    operation_kind: OperationKind
    cause: str
    source: str
    seq: int
    before_version: Optional[int]
    after_version: int
    changes: tuple[Change, ...]


@dataclass(frozen=True)
class Operation:
    kind: OperationKind
    sample: Optional[Sample] = None
    segment: Optional[AnomalySegment] = None
    note: str = ""


@dataclass(frozen=True)
class RevisionReport:
    operation_index: int
    kind: OperationKind
    revisions: tuple[Revision, ...]
    new_decisions: int
    updated_decisions: int

    @property
    def affected_keys(self) -> tuple[SampleKey, ...]:
        return tuple(SampleKey(r.source, r.seq) for r in self.revisions)


# 用于差异比较/证据快照的“实质字段”（version/basis 不入比较，
# basis 是可从其它字段重建的人类可读说明）。
_MATERIAL_FIELDS = (
    "status",
    "baseline_n",
    "mean",
    "variance",
    "included_keys",
    "excluded_keys",
    "excluded_baseline_segments",
    "active_segment_ids",
)


@dataclass(frozen=True)
class BaselineView:
    target: Sample
    included: tuple[Sample, ...]
    excluded: tuple[Sample, ...]
    excluded_baseline_segments: tuple[tuple[SampleKey, tuple[str, ...]], ...]
    active_segment_ids: tuple[str, ...]


@dataclass
class _MarkerState:
    segment: AnomalySegment
    withdrawn: bool = False
    withdraw_operation_index: Optional[int] = None
    withdraw_note: str = ""


class DriftDetector:
    """支持异常段补录/撤回与增量修订的滚动漂移检测器。"""

    def __init__(self, config: Optional[DriftConfig] = None) -> None:
        self.config = config or DriftConfig()
        self._samples: dict[str, list[Sample]] = {}
        self._index: dict[SampleKey, Sample] = {}
        self._latest: dict[SampleKey, Decision] = {}
        self._history: dict[SampleKey, list[Decision]] = {}
        self._revision_log: list[Revision] = []
        self._markers: dict[str, list[_MarkerState]] = {}
        self._operations: list[Operation] = []

    # ------------------------------------------------------------------ 输入

    def add_sample(
        self,
        source: str,
        seq: int,
        time: int,
        value: object,
        *,
        note: str = "样本到达",
    ) -> RevisionReport:
        sample = Sample(source=source, seq=seq, time=time, value=to_fraction(value))
        return self._append_sample(sample, note=note)

    def mark_anomaly(
        self,
        source: str,
        segment_id: str,
        start_time: int,
        end_time: int,
        *,
        note: str = "补录异常段",
    ) -> RevisionReport:
        segment = AnomalySegment(source, segment_id, start_time, end_time, note=note)
        return self._append_marker(segment, note=note)

    def withdraw_anomaly(
        self,
        source: str,
        segment_id: str,
        *,
        note: str = "撤回异常段",
    ) -> RevisionReport:
        op_index = len(self._operations)
        state = self._find_marker(source, segment_id)
        if state is None:
            raise KeyError(f"异常段不存在: {source}/{segment_id}")
        if state.withdrawn:
            # 重复撤回是幂等的：不产生修订。
            self._operations.append(
                Operation(
                    OperationKind.WITHDRAW_SEGMENT,
                    segment=state.segment,
                    note=note,
                )
            )
            return RevisionReport(op_index, OperationKind.WITHDRAW_SEGMENT, (), 0, 0)

        self._operations.append(
            Operation(
                OperationKind.WITHDRAW_SEGMENT,
                segment=state.segment,
                note=note,
            )
        )
        state.withdrawn = True
        state.withdraw_operation_index = op_index
        state.withdraw_note = note
        report = self._revise_range(
            source=source,
            start_time=state.segment.start_time,
            end_time=state.segment.end_time + self.config.lookback,
            operation_kind=OperationKind.WITHDRAW_SEGMENT,
            operation_index=op_index,
            cause=f"撤回异常段 {segment_id}（{note}），窗口依赖重算",
        )
        return report

    def apply_operation(self, operation: Operation) -> RevisionReport:
        if operation.kind is OperationKind.SAMPLE:
            if operation.sample is None:
                raise ValueError("SAMPLE 操作缺少 sample")
            return self._append_sample(operation.sample, note=operation.note or "样本到达")
        if operation.kind is OperationKind.ADD_SEGMENT:
            if operation.segment is None:
                raise ValueError("ADD_SEGMENT 操作缺少 segment")
            segment = operation.segment
            if operation.note and not segment.note:
                segment = replace(segment, note=operation.note)
            return self._append_marker(segment, note=operation.note or segment.note)
        if operation.kind is OperationKind.WITHDRAW_SEGMENT:
            if operation.segment is None:
                raise ValueError("WITHDRAW_SEGMENT 操作缺少 segment")
            return self.withdraw_anomaly(
                operation.segment.source,
                operation.segment.segment_id,
                note=operation.note or "撤回异常段",
            )
        raise ValueError(f"未知操作: {operation.kind!r}")

    # ------------------------------------------------------------- 内部写入

    def _append_sample(self, sample: Sample, *, note: str) -> RevisionReport:
        key = SampleKey(sample.source, sample.seq)
        if key in self._index:
            raise ValueError(f"样本键重复: {sample.source}/{sample.seq}")
        source_samples = self._samples.setdefault(sample.source, [])
        if source_samples:
            last = source_samples[-1]
            if sample.seq <= last.seq:
                raise ValueError("同一 source 的 seq 必须严格递增")
            if sample.time < last.time:
                raise ValueError("同一 source 的事件时间必须非递减（seq 为到达顺序）")

        op_index = len(self._operations)
        self._operations.append(Operation(OperationKind.SAMPLE, sample=sample, note=note))
        insert_at = len(source_samples)
        for idx in range(len(source_samples) - 1, -1, -1):
            if source_samples[idx].time <= sample.time:
                insert_at = idx + 1
                break
        else:
            insert_at = 0
        source_samples.insert(insert_at, sample)
        self._index[key] = sample

        revision = self._evaluate(sample)
        self._latest[key] = revision
        self._history[key] = [revision]
        log = Revision(
            operation_index=op_index,
            operation_kind=OperationKind.SAMPLE,
            cause=note,
            source=sample.source,
            seq=sample.seq,
            before_version=None,
            after_version=1,
            changes=(Change("created", None, _material_tuple(revision)),),
        )
        self._revision_log.append(log)
        return RevisionReport(op_index, OperationKind.SAMPLE, (log,), 1, 0)

    def _append_marker(self, segment: AnomalySegment, *, note: str) -> RevisionReport:
        op_index = len(self._operations)
        state = self._find_marker(segment.source, segment.segment_id)
        if state is not None:
            if state.withdrawn:
                raise ValueError(
                    f"segment_id 已被撤回且不可复用: {segment.source}/{segment.segment_id}"
                )
            if state.segment != segment:
                raise ValueError(
                    f"重复 segment_id 的区间不一致: {segment.source}/{segment.segment_id}"
                )
            # 完全重复的标记是幂等操作：记录操作，但不产生新的修订。
            self._operations.append(
                Operation(OperationKind.ADD_SEGMENT, segment=segment, note=note)
            )
            return RevisionReport(op_index, OperationKind.ADD_SEGMENT, (), 0, 0)

        self._operations.append(
            Operation(OperationKind.ADD_SEGMENT, segment=segment, note=note)
        )
        marker_list = self._markers.setdefault(segment.source, [])
        new_state = _MarkerState(segment=segment)
        marker_list.append(new_state)
        marker_list.sort(key=lambda item: item.segment.start_time)

        report = self._revise_range(
            source=segment.source,
            start_time=segment.start_time,
            end_time=segment.end_time + self.config.lookback,
            operation_kind=OperationKind.ADD_SEGMENT,
            operation_index=op_index,
            cause=f"补录异常段 {segment.segment_id}（{note}），窗口依赖重算",
        )
        return report

    # ------------------------------------------------------------- 内部查询

    def _find_marker(self, source: str, segment_id: str) -> Optional[_MarkerState]:
        for state in self._markers.get(source, []):
            if state.segment.segment_id == segment_id:
                return state
        return None

    def _active_segments(self, source: str, time: int) -> tuple[AnomalySegment, ...]:
        active = [
            state.segment
            for state in self._markers.get(source, [])
            if not state.withdrawn and state.segment.covers_time(time)
        ]
        active.sort(key=lambda segment: segment.segment_id)
        return tuple(active)

    def _baseline_view(self, sample: Sample) -> BaselineView:
        source = sample.source
        candidates = self._samples.get(source, ())
        start_time = sample.time - self.config.lookback
        end_time = sample.time
        included: list[Sample] = []
        excluded: list[Sample] = []
        excluded_segments: list[tuple[Sample, tuple[str, ...]]] = []
        active_ids = set()
        for candidate in candidates:
            if not (start_time <= candidate.time < end_time):
                continue
            active = self._active_segments(source, candidate.time)
            if active:
                segment_ids = tuple(segment.segment_id for segment in active)
                excluded.append(candidate)
                excluded_segments.append((candidate, segment_ids))
            else:
                included.append(candidate)
        for segment in self._active_segments(source, sample.time):
            active_ids.add(segment.segment_id)
        included.sort(key=lambda item: (item.time, item.seq))
        excluded.sort(key=lambda item: (item.time, item.seq))
        excluded_segments.sort(key=lambda item: (item[0].time, item[0].seq))
        return BaselineView(
            target=sample,
            included=tuple(included),
            excluded=tuple(excluded),
            excluded_baseline_segments=tuple(
                (SampleKey(item.source, item.seq), segment_ids)
                for item, segment_ids in excluded_segments
            ),
            active_segment_ids=tuple(sorted(active_ids)),
        )

    def _evaluate(self, sample: Sample) -> Decision:
        view = self._baseline_view(sample)
        active_ids = view.active_segment_ids
        included_keys = tuple(SampleKey(item.source, item.seq) for item in view.included)
        excluded_keys = tuple(SampleKey(item.source, item.seq) for item in view.excluded)

        mean: Optional[Fraction] = None
        variance: Optional[Fraction] = None
        n = len(view.included)
        if n:
            values = [item.value for item in view.included]
            total = sum(values, Fraction(0))
            mean = total / n
            variance = sum((value - mean) ** 2 for value in values) / n

        if active_ids:
            status = DriftStatus.EXCLUDED
            basis = (
                f"样本位于有效异常段 {','.join(active_ids)} 内，"
                f"不作漂移判定；同时记录剔除后的有效基线 n={n}"
            )
        elif n == 0:
            status = DriftStatus.UNDECIDABLE
            basis = "有效基线为空（窗口内无样本或样本全部被异常段排除）"
        elif n < self.config.min_baseline:
            status = DriftStatus.INSUFFICIENT
            basis = f"有效基线 n={n}，少于 min_baseline={self.config.min_baseline}"
        else:
            delta = sample.value - mean
            threshold = self.config.threshold
            # 精确、无开方：严格 3σ，delta^2 > threshold^2 * variance。
            if delta * delta > threshold * threshold * variance:
                status = DriftStatus.DRIFT
                comparator = ">"
            else:
                status = DriftStatus.NORMAL
                comparator = "<="
            basis = (
                f"严格 {threshold.numerator}/{threshold.denominator}σ: "
                f"n={n}, mean={mean}, variance={variance}, "
                f"delta={delta}, delta^2 {comparator} "
                f"threshold^2*variance={threshold * threshold * variance}"
            )

        return Decision(
            source=sample.source,
            seq=sample.seq,
            time=sample.time,
            value=sample.value,
            status=status,
            baseline_n=n,
            mean=mean,
            variance=variance,
            included_keys=included_keys,
            excluded_keys=excluded_keys,
            excluded_baseline_segments=view.excluded_baseline_segments,
            active_segment_ids=active_ids,
            basis=basis,
            version=1,  # 写入历史前由修订流程改成真实版本
        )

    def _revise_range(
        self,
        *,
        source: str,
        start_time: int,
        end_time: int,
        operation_kind: OperationKind,
        operation_index: int,
        cause: str,
    ) -> RevisionReport:
        affected = [
            sample
            for sample in self._samples.get(source, [])
            if start_time <= sample.time <= end_time
        ]
        affected.sort(key=lambda item: (item.time, item.seq))

        revisions: list[Revision] = []
        new_count = 0
        update_count = 0
        for sample in affected:
            key = SampleKey(sample.source, sample.seq)
            new_decision = self._evaluate(sample)
            old = self._latest.get(key)
            if old is None:
                stored = replace(new_decision, version=1)
                self._latest[key] = stored
                self._history.setdefault(key, []).append(stored)
                revision = Revision(
                    operation_index=operation_index,
                    operation_kind=operation_kind,
                    cause=cause,
                    source=source,
                    seq=sample.seq,
                    before_version=None,
                    after_version=1,
                    changes=(Change("created", None, _material_tuple(stored)),),
                )
                new_count += 1
            else:
                if _material_equal(old, new_decision):
                    continue
                next_version = old.version + 1
                stored = replace(new_decision, version=next_version)
                self._latest[key] = stored
                self._history[key].append(stored)
                changes = tuple(_diff_decisions(old, stored))
                revision = Revision(
                    operation_index=operation_index,
                    operation_kind=operation_kind,
                    cause=cause,
                    source=source,
                    seq=sample.seq,
                    before_version=old.version,
                    after_version=next_version,
                    changes=changes,
                )
                update_count += 1
            revisions.append(revision)
            self._revision_log.append(revision)

        return RevisionReport(
            operation_index=operation_index,
            kind=operation_kind,
            revisions=tuple(revisions),
            new_decisions=new_count,
            updated_decisions=update_count,
        )

    # -------------------------------------------------------------- 只读接口

    def decision(self, source: str, seq: int) -> Decision:
        key = SampleKey(source, seq)
        try:
            return self._latest[key]
        except KeyError as exc:
            raise KeyError(f"样本不存在: {source}/{seq}") from exc

    def decisions(self, source: Optional[str] = None) -> tuple[Decision, ...]:
        results = [
            decision
            for key, decision in self._latest.items()
            if source is None or key.source == source
        ]
        results.sort(key=lambda decision: (decision.source, decision.seq))
        return tuple(results)

    def decision_history(self, source: str, seq: int) -> tuple[Decision, ...]:
        key = SampleKey(source, seq)
        try:
            return tuple(self._history[key])
        except KeyError as exc:
            raise KeyError(f"样本不存在: {source}/{seq}") from exc

    def revision_log(self) -> tuple[Revision, ...]:
        return tuple(self._revision_log)

    @property
    def operations(self) -> tuple[Operation, ...]:
        return tuple(self._operations)

    def baseline_for(self, source: str, seq: int) -> BaselineView:
        sample = self._index[SampleKey(source, seq)]
        return self._baseline_view(sample)

    def revisions_for_key(self, source: str, seq: int) -> tuple[Revision, ...]:
        return tuple(
            revision
            for revision in self._revision_log
            if revision.source == source and revision.seq == seq
        )

    def state_snapshot(self) -> dict[SampleKey, Decision]:
        return dict(self._latest)

    # ----------------------------------------------------------------- 重放

    @classmethod
    def replay(cls, operations: Iterable[Operation], config: Optional[DriftConfig] = None) -> "DriftDetector":
        detector = cls(config=config)
        for operation in operations:
            detector.apply_operation(operation)
        return detector


def _material_tuple(decision: Decision) -> tuple[object, ...]:
    return tuple(getattr(decision, field_name) for field_name in _MATERIAL_FIELDS)


def _material_equal(left: Decision, right: Decision) -> bool:
    return _material_tuple(left) == _material_tuple(right)


def _diff_decisions(before: Decision, after: Decision) -> list[Change]:
    changes: list[Change] = []
    for field_name in _MATERIAL_FIELDS:
        old_value = getattr(before, field_name)
        new_value = getattr(after, field_name)
        if old_value != new_value:
            changes.append(Change(field_name, old_value, new_value))
    return changes


def replay_matches(detector: DriftDetector, *, compare_logs: bool = True) -> bool:
    """全量重放操作日志，比较最终判定及（可选）完整修订链。"""
    replayed = DriftDetector.replay(detector.operations, config=detector.config)
    left = detector.state_snapshot()
    right = replayed.state_snapshot()
    if set(left) != set(right):
        return False
    for key in left:
        if _material_tuple(left[key]) != _material_tuple(right[key]):
            return False
        if left[key].version != right[key].version:
            return False
    if compare_logs:
        left_logs = detector.revision_log()
        right_logs = replayed.revision_log()
        if len(left_logs) != len(right_logs):
            return False
        for old, new in zip(left_logs, right_logs, strict=True):
            if old != new:
                return False
    return True


def assert_replay_matches(detector: DriftDetector) -> None:
    """逐步重放失败时给出首个差异，便于测试定位。"""
    replayed = DriftDetector(config=detector.config)
    for index, operation in enumerate(detector.operations):
        replayed.apply_operation(operation)
        current = DriftDetector.replay(detector.operations[: index + 1], detector.config)
        if current.state_snapshot().keys() != replayed.state_snapshot().keys():
            raise AssertionError(f"第 {index} 步后样本键集合不一致")
        for key, expected in replayed.state_snapshot().items():
            actual = current.state_snapshot()[key]
            if actual != expected:
                raise AssertionError(
                    f"第 {index} 步后 {key} 不一致:\n"
                    f"增量={actual!r}\n重放={expected!r}"
                )
        if current.revision_log() != replayed.revision_log():
            raise AssertionError(f"第 {index} 步后修订链不一致")
    if not replay_matches(detector):
        raise AssertionError("最终全量重放与增量状态不一致")
