"""贡献索引:按 (行, 列, 内维 k) 维护中间贡献 (值, 来源计数)。

- 每个输出单元的每个 k 路径单独记账,支持精确撤回与相互抵消。
- 掩码删除只把单元移入 hidden 区,贡献继续维护,重新加入即可恢复。
- 热点行的中间贡献超过阈值时溢写到临时文件,限制常驻内存。
"""

import os
import pickle
import tempfile

from .errors import RetractionError, SpillError


class SpillStore:
    """基于临时目录的溢写存储,写操作原子化(临时文件 + rename)。"""

    def __init__(self, directory=None):
        self._tmp = tempfile.TemporaryDirectory(prefix="mmul_spill_", dir=directory)
        self.fail_writes = False

    @property
    def directory(self):
        return self._tmp.name

    def write(self, key, obj):
        if self.fail_writes:
            raise SpillError("溢写失败(注入故障)")
        path = os.path.join(self._tmp.name, key)
        tmp_path = path + ".tmp"
        with open(tmp_path, "wb") as fh:
            pickle.dump(obj, fh, protocol=4)
        os.replace(tmp_path, path)

    def read(self, key):
        with open(os.path.join(self._tmp.name, key), "rb") as fh:
            return pickle.load(fh)

    def remove(self, key):
        try:
            os.remove(os.path.join(self._tmp.name, key))
        except FileNotFoundError:
            pass

    def keys(self):
        return [
            name
            for name in os.listdir(self._tmp.name)
            if not name.endswith(".tmp")
        ]

    def cleanup(self):
        self._tmp.cleanup()


class ContributionIndex:
    """维护可见单元的中间贡献与单元合计,热点行可溢写。"""

    def __init__(self, spill_store, row_threshold=1024):
        if row_threshold < 1:
            raise ValueError("row_threshold 必须 >= 1")
        self.spill = spill_store
        self.row_threshold = row_threshold
        self._sums = {}
        self._sources = {}
        self._spilled = set()
        self._hidden = {}

    @staticmethod
    def _key(i):
        return f"row_{i}.pkl"

    def is_hidden(self, i, j):
        return (i, j) in self._hidden

    @staticmethod
    def _validated(entry, value, delta_count):
        old_value = entry[0] if entry else 0
        old_count = entry[1] if entry else 0
        new_value = old_value + value
        new_count = old_count + delta_count
        if new_count < 0:
            raise RetractionError("贡献来源计数为负: 撤回超过插入")
        if new_count == 0 and new_value != 0:
            raise RetractionError(f"贡献撤回不匹配: 剩余值 {new_value}")
        return new_value, new_count

    def _load_row(self, i):
        if i in self._spilled:
            payload = self.spill.read(self._key(i))
            self._sums[i] = payload["sums"]
            self._sources[i] = payload["sources"]
            self._spilled.discard(i)
            self.spill.remove(self._key(i))

    def _maybe_spill(self, i):
        sources = self._sources.get(i)
        if not sources:
            return
        entries = sum(len(cell) for cell in sources.values())
        if entries > self.row_threshold:
            payload = {"sums": self._sums.get(i, {}), "sources": sources}
            self.spill.write(self._key(i), payload)
            self._sums.pop(i, None)
            self._sources.pop(i, None)
            self._spilled.add(i)

    def add_source(self, i, j, k, value, delta_count):
        """对路径 (i, k, j) 施加一次贡献增量(可正可负)。"""
        hidden = self._hidden.get((i, j))
        if hidden is not None:
            new_value, new_count = self._validated(
                hidden[1].get(k), value, delta_count
            )
            if new_count == 0:
                del hidden[1][k]
            else:
                hidden[1][k] = [new_value, new_count]
            hidden[0] += value
            if not hidden[1]:
                del self._hidden[(i, j)]
            return
        self._load_row(i)
        sources = self._sources.get(i)
        cell = sources.get(j) if sources is not None else None
        entry = cell.get(k) if cell else None
        new_value, new_count = self._validated(entry, value, delta_count)
        if sources is None:
            sources = self._sources.setdefault(i, {})
        if cell is None:
            cell = sources.setdefault(j, {})
        if new_count == 0:
            del cell[k]
        else:
            cell[k] = [new_value, new_count]
        if not cell:
            del sources[j]
        if not sources:
            del self._sources[i]
        sums = self._sums.setdefault(i, {})
        total = sums.get(j, 0) + value
        if total == 0 and j not in sources:
            sums.pop(j, None)
        else:
            sums[j] = total
        if not sums and i not in self._sources:
            self._sums.pop(i, None)
        if delta_count > 0:
            self._maybe_spill(i)

    def hide_cell(self, i, j):
        """掩码删除:把单元贡献移入 hidden 区,仅影响可见性。"""
        self._load_row(i)
        sums = self._sums.get(i)
        sources = self._sources.get(i)
        total = sums.get(j, 0) if sums else 0
        cell = sources.get(j) if sources else None
        if sums is not None:
            sums.pop(j, None)
            if not sums:
                del self._sums[i]
        if sources is not None:
            sources.pop(j, None)
            if not sources:
                del self._sources[i]
        if cell:
            self._hidden[(i, j)] = [total, cell]

    def unhide_cell(self, i, j):
        """掩码恢复:若 hidden 区保留了贡献则原样恢复。"""
        hidden = self._hidden.pop((i, j), None)
        if hidden is None:
            return False
        total, cell = hidden
        self._load_row(i)
        if cell:
            sources = self._sources.setdefault(i, {})
            sources[j] = cell
            if total != 0:
                sums = self._sums.setdefault(i, {})
                sums[j] = total
            self._maybe_spill(i)
        return True

    def source_entry(self, i, j, k):
        hidden = self._hidden.get((i, j))
        if hidden is not None:
            entry = hidden[1].get(k)
        else:
            self._load_row(i)
            entry = self._sources.get(i, {}).get(j, {}).get(k)
        return tuple(entry) if entry else None

    def _all_rows(self):
        return set(self._sums) | self._spilled

    def materialize(self):
        """按行确定排序输出非零可见单元。"""
        out = []
        for i in sorted(self._all_rows()):
            if i in self._spilled:
                sums = self.spill.read(self._key(i))["sums"]
            else:
                sums = self._sums.get(i, {})
            for j in sorted(sums):
                value = sums[j]
                if value != 0:
                    out.append((i, j, value))
        return tuple(out)

    def iter_sources(self):
        for i, sources in self._sources.items():
            for j, cell in sources.items():
                for k, (value, count) in cell.items():
                    yield (i, j, k, value, count)
        for i in sorted(self._spilled):
            payload = self.spill.read(self._key(i))
            for j, cell in payload["sources"].items():
                for k, (value, count) in cell.items():
                    yield (i, j, k, value, count)
        for (i, j), (_total, cell) in self._hidden.items():
            for k, (value, count) in cell.items():
                yield (i, j, k, value, count)

    def stats(self):
        resident = sum(
            len(cell) for src in self._sources.values() for cell in src.values()
        )
        spilled = 0
        for i in self._spilled:
            payload = self.spill.read(self._key(i))
            spilled += sum(len(cell) for cell in payload["sources"].values())
        hidden = sum(len(cell) for _t, cell in self._hidden.values())
        return {
            "resident_rows": len(self._sums),
            "spilled_rows": len(self._spilled),
            "resident_source_entries": resident,
            "spilled_source_entries": spilled,
            "hidden_cells": len(self._hidden),
            "hidden_source_entries": hidden,
            "total_source_entries": resident + spilled + hidden,
        }

    def check_against(self, A, B, mask):
        """用输入矩阵重算全部贡献,校验索引既无缺失也无孤儿。"""
        expected = {}
        for i, arow in A.rows_items():
            for k, (aw, ac) in arow.items():
                for j, (bw, bc) in B.row(k).items():
                    if mask.contains(i, j) or self.is_hidden(i, j):
                        entry = expected.setdefault((i, j, k), [0, 0])
                        entry[0] += aw * bw
                        entry[1] += ac * bc
        actual = {}
        for (i, j, k, value, count) in self.iter_sources():
            actual[(i, j, k)] = [value, count]
        if expected != actual:
            missing = {k: v for k, v in expected.items() if actual.get(k) != v}
            orphan = {k: v for k, v in actual.items() if expected.get(k) != v}
            raise AssertionError(f"贡献索引不一致: 缺失={missing} 孤儿={orphan}")
        cell_sums = {}
        for (i, j, _k), (value, _c) in ((key, v) for key, v in actual.items()):
            cell_sums[(i, j)] = cell_sums.get((i, j), 0) + value
        for i in self._all_rows():
            if i in self._spilled:
                sums = self.spill.read(self._key(i))["sums"]
            else:
                sums = self._sums.get(i, {})
            for j, total in sums.items():
                if total != cell_sums.get((i, j), 0):
                    raise AssertionError(f"单元合计不一致: ({i}, {j})")
        for (i, j), (total, _cell) in self._hidden.items():
            if total != cell_sums.get((i, j), 0):
                raise AssertionError(f"隐藏单元合计不一致: ({i}, {j})")
