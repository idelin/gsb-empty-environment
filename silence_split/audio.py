"""稀疏音频存储:按区间写入样本数组。

未写入的区域是"缺口"(未知),与写入零值的"真实静音"严格区分:
检测器在缺口处停滞等待,而真实静音参与静音确认。
"""
from __future__ import annotations


class AudioStore:
    def __init__(self, sample_rate):
        self.sample_rate = sample_rate
        self._ranges = []  # [start, end, samples],按起点排序且互不重叠

    def write(self, start, samples, overwrite=False):
        samples = list(samples)
        end = start + len(samples)
        if start < 0:
            raise ValueError("negative start")
        if overwrite:
            self.erase(start, end)
        else:
            for s, e, _ in self._ranges:
                if s < end and start < e:
                    raise ValueError(f"overlap with existing range [{s}, {e})")
        if samples:
            self._ranges.append([start, end, samples])
            self._ranges.sort(key=lambda r: r[0])
            self._merge_adjacent()

    def erase(self, start, end):
        """抹除区间,使其回到缺口(未知)状态。"""
        new = []
        for s, e, data in self._ranges:
            if e <= start or s >= end:
                new.append([s, e, data])
                continue
            if s < start:
                new.append([s, start, data[:start - s]])
            if e > end:
                new.append([end, e, data[end - s:]])
        self._ranges = new

    def _merge_adjacent(self):
        merged = []
        for r in self._ranges:
            if merged and merged[-1][1] == r[0]:
                merged[-1][1] = r[1]
                merged[-1][2].extend(r[2])
            else:
                merged.append(list(r))
        self._ranges = merged

    def contiguous_prefix(self):
        """从 0 起连续覆盖的采样点数;首个缺口即截断。"""
        if self._ranges and self._ranges[0][0] == 0:
            return self._ranges[0][1]
        return 0

    def is_covered(self, start, end):
        return any(s <= start and end <= e for s, e, _ in self._ranges)

    def read(self, start, end):
        for s, e, data in self._ranges:
            if s <= start and end <= e:
                return data[start - s:end - s]
        raise KeyError(f"range [{start}, {end}) not covered")

    @property
    def ranges(self):
        return [(s, e) for s, e, _ in self._ranges]

    @property
    def written_end(self):
        return max((e for _, e, _ in self._ranges), default=0)


class MultiTrackStore:
    """多轨存储;各轨共享采样点坐标,连续覆盖前缀取各轨最小值。"""

    def __init__(self, tracks, sample_rate):
        self.tracks = tuple(tracks)
        self.sample_rate = sample_rate
        self._stores = {t: AudioStore(sample_rate) for t in self.tracks}

    def __getitem__(self, track):
        return self._stores[track]

    def contiguous_prefix(self):
        return min(self._stores[t].contiguous_prefix() for t in self.tracks)
