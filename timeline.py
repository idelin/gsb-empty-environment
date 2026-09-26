"""变长块样本-时间映射核心库（仅标准库，Python 3.11）。

样本数组与采样率由本地夹具提供，不接触任何音频设备。

时间模型：
    块 b 的有效起点 effective_start = nominal_start + calibration_offset
    样本 i 覆盖半开区间 [effective_start + i/rate, effective_start + (i+1)/rate)

重叠裁决：版本高者优先；版本相同按 block_id 字典序大者优先（确定性规则）。
样本归属：块的拥有区间（自身区间减去高优先级块区间）按时间不相交；
          样本 i 归属于其【中点】所在区间的块，保证任何样本只被认领一次。
声明延迟：declared_latency 仅作元数据声明记录，不参与换算；
          如需应用，调用方可通过校准偏移（calibration offset）折入。
数值容差：TIME_TOLERANCE（秒），样本域换算时按采样率缩放。
"""
from __future__ import annotations

import bisect
import math
from dataclasses import dataclass
from typing import Dict, List, Tuple

TIME_TOLERANCE = 1e-9  # 声明的数值容差（秒）


@dataclass(frozen=True)
class Block:
    block_id: str
    sample_rate: float
    samples: Tuple[float, ...]
    nominal_start: float          # 校准前的标称起点（秒）
    version: int = 0              # 重叠裁决优先级：高者胜
    declared_latency: float = 0.0  # 声明的采集延迟（秒），仅元数据

    @property
    def count(self) -> int:
        return len(self.samples)

    @property
    def duration(self) -> float:
        return self.count / self.sample_rate


@dataclass(frozen=True)
class RoundingEvidence:
    """时间->样本换算的舍入证据。"""
    block_id: str
    time: float
    position: float   # 换算出的（小数）样本位置
    index: int        # 容差修正后取整的样本下标
    residue: float    # 真实小数部分 position - floor(position)


@dataclass(frozen=True)
class Slice:
    """版本裁决后的不相交切片（时间区间为拥有区间）；保留舍入证据。"""
    block_id: str
    version: int
    sample_start: int
    sample_end: int              # 排他
    t_start: float
    t_end: float
    raw_start_pos: float         # 舍入前的小数样本位置（证据）
    raw_end_pos: float


@dataclass(frozen=True)
class Hole:
    t_start: float
    t_end: float


@dataclass(frozen=True)
class Segment:
    block_id: str
    version: int
    sample_start: int
    sample_end: int
    t_start: float
    t_end: float
    samples: Tuple[float, ...]


@dataclass(frozen=True)
class ReadResult:
    t0: float
    t1: float
    segments: Tuple[Segment, ...]
    holes: Tuple[Hole, ...]


def _subtract(regions: List[Tuple[float, float]],
              cut: Tuple[float, float]) -> List[Tuple[float, float]]:
    """从区间列表中扣除 cut，返回剩余不相交区间。"""
    ca, cb = cut
    out: List[Tuple[float, float]] = []
    for a, b in regions:
        if cb <= a or ca >= b:
            out.append((a, b))
            continue
        if a < ca:
            out.append((a, min(ca, b)))
        if cb < b:
            out.append((max(cb, a), b))
    return out


class _Generation:
    """不可变快照：每次增删块或校准变更产生一个完整的代。

    旧游标持有旧代引用，读取结果稳定；新代构建完成后由 Timeline 一次性换入。
    """

    def __init__(self, blocks: Dict[str, Block], calibrations: Dict[str, float]):
        self.blocks = dict(blocks)
        self.calibrations = dict(calibrations)
        self.intervals: Dict[str, Tuple[float, float]] = {}
        for bid, blk in self.blocks.items():
            start = blk.nominal_start + self.calibrations.get(bid, 0.0)
            self.intervals[bid] = (start, start + blk.duration)
        self.slices = self._build_slices()
        # 一级索引：切片起点数组（显式按时间排序，不假设块插入/校准顺序单调）
        self._starts = [s.t_start for s in self.slices]
        self._ends = [s.t_end for s in self.slices]
        # 样本最多越出拥有区间半个采样周期，用于二分定位的安全余量
        self._max_half = max((0.5 / b.sample_rate for b in self.blocks.values()),
                             default=0.0)
        self.holes = self._build_holes()

    def _priority(self, bid: str) -> Tuple[int, str]:
        blk = self.blocks[bid]
        return (blk.version, bid)

    def _build_slices(self) -> Tuple[Slice, ...]:
        owned: Dict[str, List[Tuple[float, float]]] = {}
        for bid in self.blocks:
            regions = [self.intervals[bid]]
            for oid in self.blocks:
                if oid == bid or self._priority(oid) <= self._priority(bid):
                    continue
                regions = _subtract(regions, self.intervals[oid])
            owned[bid] = regions
        slices: List[Slice] = []
        for bid, regions in owned.items():
            blk = self.blocks[bid]
            base = self.intervals[bid][0]
            rate = blk.sample_rate
            tol = TIME_TOLERANCE * rate  # 容差换算到样本域
            for a, b in regions:
                raw_a = (a - base) * rate
                raw_b = (b - base) * rate
                # 中点归属：样本 i 属于本区间 <=> raw_a <= i+0.5 < raw_b
                i0 = max(0, math.ceil(raw_a - 0.5 - tol))
                i1 = min(blk.count, math.ceil(raw_b - 0.5 - tol))
                if i1 <= i0:
                    continue
                slices.append(Slice(bid, blk.version, i0, i1,
                                    a, b,
                                    raw_a, raw_b))
        slices.sort(key=lambda s: (s.t_start, s.block_id))
        return tuple(slices)

    def _build_holes(self) -> Tuple[Hole, ...]:
        holes: List[Hole] = []
        for prev, nxt in zip(self.slices, self.slices[1:]):
            if nxt.t_start > prev.t_end + TIME_TOLERANCE:
                holes.append(Hole(prev.t_end, nxt.t_start))
        return tuple(holes)

    # ---- 按时间读取：返回有效样本段与空洞区间 ----
    def read(self, t0: float, t1: float) -> ReadResult:
        if t1 < t0:
            raise ValueError("t1 must be >= t0")
        # 一级索引二分定位候选切片（样本可越出拥有区间半个周期，留安全余量），
        # 二级在切片内做样本级换算。只按显式排序后的时间索引，不假设单调插入。
        lo = bisect.bisect_right(
            self._ends, t0 - self._max_half - TIME_TOLERANCE)
        limit = t1 + self._max_half + TIME_TOLERANCE
        segments: List[Segment] = []
        j = lo
        while j < len(self.slices) and self._starts[j] < limit:
            sl = self.slices[j]
            j += 1
            blk = self.blocks[sl.block_id]
            rate = blk.sample_rate
            base = self.intervals[sl.block_id][0]
            tol = TIME_TOLERANCE * rate
            # 样本区间与 [t0, t1) 相交即纳入（样本可越出拥有区间边界）
            i0 = max(sl.sample_start, math.floor((t0 - base) * rate + tol))
            i1 = min(sl.sample_end, math.ceil((t1 - base) * rate - tol))
            if i1 <= i0:
                continue
            seg_t0 = base + i0 / rate
            seg_t1 = base + i1 / rate
            segments.append(Segment(sl.block_id, sl.version, i0, i1,
                                    seg_t0, seg_t1,
                                    tuple(blk.samples[i0:i1])))
        # 空洞 = 查询区间减去有效样本区间的并集（与逐样本参照一致）
        holes: List[Hole] = []
        covered = sorted((max(t0, s.t_start), min(t1, s.t_end))
                         for s in segments)
        cursor = t0
        for a, b in covered:
            if a > cursor + TIME_TOLERANCE:
                holes.append(Hole(cursor, a))
            cursor = max(cursor, b)
        if cursor < t1 - TIME_TOLERANCE:
            holes.append(Hole(cursor, t1))
        return ReadResult(t0, t1, tuple(segments), tuple(holes))

    # ---- 双向查询：样本范围 -> 时间范围（使用该块自身采样率） ----
    def sample_to_time(self, block_id: str, i0: int, i1: int = None):
        blk = self.blocks[block_id]
        if i1 is None:
            i1 = i0 + 1
        if not (0 <= i0 <= i1 <= blk.count):
            raise ValueError("sample range out of bounds")
        base = self.intervals[block_id][0]
        rate = blk.sample_rate
        return (base + i0 / rate, base + i1 / rate)

    # ---- 双向查询：时间 -> 样本下标（附舍入证据） ----
    def time_to_sample(self, block_id: str, t: float) -> RoundingEvidence:
        blk = self.blocks[block_id]
        base = self.intervals[block_id][0]
        pos = (t - base) * blk.sample_rate
        idx = math.floor(pos + TIME_TOLERANCE * blk.sample_rate)
        return RoundingEvidence(block_id, t, pos, idx, pos - math.floor(pos))


class Timeline:
    """可变时间线：增删块、设置校准；每次变更原子换入新代。"""

    def __init__(self):
        self._blocks: Dict[str, Block] = {}
        self._calibrations: Dict[str, float] = {}
        self._gen = _Generation({}, {})

    def add_block(self, block: Block) -> None:
        if block.block_id in self._blocks:
            raise ValueError(f"duplicate block_id: {block.block_id}")
        self._blocks[block.block_id] = block
        self._commit()

    def remove_block(self, block_id: str) -> None:
        """撤回一个块：其遮蔽的低版本样本重新生效。"""
        del self._blocks[block_id]
        self._calibrations.pop(block_id, None)
        self._commit()

    def set_calibration(self, block_id: str, offset: float) -> None:
        """新增/更新校准偏移；可改变历史块映射。"""
        if block_id not in self._blocks:
            raise KeyError(block_id)
        self._calibrations[block_id] = offset
        self._commit()

    def _commit(self) -> None:
        # 原子更新：新代完整构建后一次性替换引用，旧代对象不受影响
        self._gen = _Generation(self._blocks, self._calibrations)

    @property
    def generation(self) -> _Generation:
        return self._gen

    def cursor(self) -> "Cursor":
        return Cursor(self._gen)

    def read(self, t0: float, t1: float) -> ReadResult:
        return self._gen.read(t0, t1)

    def sample_to_time(self, block_id, i0, i1=None):
        return self._gen.sample_to_time(block_id, i0, i1)

    def time_to_sample(self, block_id, t) -> RoundingEvidence:
        return self._gen.time_to_sample(block_id, t)

    @property
    def slices(self):
        return self._gen.slices

    @property
    def holes(self):
        return self._gen.holes


class Cursor:
    """绑定固定代的稳定游标：后续校准/增删不影响其读取。"""

    def __init__(self, generation: _Generation):
        self._gen = generation

    def read(self, t0: float, t1: float) -> ReadResult:
        return self._gen.read(t0, t1)

    def sample_to_time(self, block_id, i0, i1=None):
        return self._gen.sample_to_time(block_id, i0, i1)

    def time_to_sample(self, block_id, t) -> RoundingEvidence:
        return self._gen.time_to_sample(block_id, t)

    @property
    def slices(self):
        return self._gen.slices

    @property
    def holes(self):
        return self._gen.holes
