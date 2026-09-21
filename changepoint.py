"""增量变化点索引。

数据流中的事件以 (source, seq) 唯一标识，值按有理数(Fraction)处理。
候选点是相邻事件之间的分界：对分界 i，取左侧最近 window 个样本与右侧最近
window 个样本，两侧样本数都 >= min_samples 时，若两侧均值差的绝对值
>= threshold（有理数精确比较，相等算达标），则判定为变化点。

支持的操作：
  add(source, seq, value)     追加事件；同 (source, seq) 同值为重复，不增加样本量；
                              同标识不同值视为对该历史记录的更正（原地改值）。
  correct(source, seq, value) 显式更正一条历史记录的值。
  retract(source, seq)        撤回一条事件。
  apply_batch(ops)            分批输入，op 为 ('add'|'correct'|'retract', ...) 元组。

每次修改只重算窗口可能覆盖被改样本的候选点（约 2*window 个），并返回
ChangeDelta 列表：kind='add' 表示新增/更新的变化点，kind='retract' 表示
失去支持、必须撤掉的旧结果。变化点以 boundary=(左事件, 右事件) 稳定标识，
证据包含左右样本集合与两侧均值。
"""

from collections import namedtuple
from fractions import Fraction

__all__ = [
    'ChangePointIndex', 'ChangePointConsumer', 'ChangeDelta', 'full_recompute',
]


class ChangeDelta(namedtuple('ChangeDelta', ('kind', 'boundary', 'index', 'evidence'))):
    """一条增量结果。

    kind: 'add'（新增或证据更新的变化点）或 'retract'（失去支持，需撤销）。
    boundary: (left_key, right_key)，分界两侧事件的 (source, seq) 标识。
    index: 变更发生时的分界位置（retract 时为旧位置）。
    evidence: 左右证据集合，含 left/right 样本键、两侧均值与差值。
    """
    __slots__ = ()


def _same_evidence(a, b):
    return (a['left'] == b['left'] and a['right'] == b['right']
            and a['left_mean'] == b['left_mean']
            and a['right_mean'] == b['right_mean']
            and a['diff'] == b['diff'])


class ChangePointIndex:
    def __init__(self, window, min_samples, threshold):
        if window < 1:
            raise ValueError('window must be >= 1')
        if min_samples < 1:
            raise ValueError('min_samples must be >= 1')
        self.window = int(window)
        self.min_samples = int(min_samples)
        self.threshold = Fraction(threshold)
        self._order = []      # 事件键的到达顺序
        self._values = {}     # 键 -> Fraction
        self._pos = {}        # 键 -> 当前下标
        self._detected = {}   # boundary -> evidence（不含 index）
        self.touched = 0      # 仪表：候选点评估次数（衡量增量计算量）

    def __len__(self):
        return len(self._order)

    # ---- 候选点评估 ----

    def _evaluate(self, i):
        """评估分界 i（在 order[i-1] 与 order[i] 之间），返回 evidence 或 None。"""
        self.touched += 1
        n = len(self._order)
        w = self.window
        left = self._order[max(0, i - w):i]
        right = self._order[i:min(n, i + w)]
        if len(left) < self.min_samples or len(right) < self.min_samples:
            return None
        lmean = sum((self._values[k] for k in left), Fraction(0)) / len(left)
        rmean = sum((self._values[k] for k in right), Fraction(0)) / len(right)
        diff = rmean - lmean
        if diff < 0:
            diff = -diff
        if diff < self.threshold:
            return None
        return {
            'boundary': (left[-1], right[0]),
            'left': tuple(left),
            'right': tuple(right),
            'left_mean': lmean,
            'right_mean': rmean,
            'diff': diff,
        }

    def _snapshot(self, lo, hi):
        """取出当前下标落在 [lo, hi] 内已检出变化点的 (index, evidence) 快照。"""
        snap = {}
        for boundary, ev in self._detected.items():
            i = self._pos[boundary[1]]
            if lo <= i <= hi:
                snap[boundary] = (i, ev)
        return snap

    def _refresh(self, snapshot, lo, hi):
        """重算新下标范围 [lo, hi] 内的候选点，与快照 diff 后返回 ChangeDelta 列表。"""
        n = len(self._order)
        lo = max(0, lo)
        hi = min(n, hi)
        found = {}
        for i in range(lo, hi + 1):
            ev = self._evaluate(i)
            if ev is not None:
                found[ev['boundary']] = (i, ev)
        deltas = []
        for boundary, (old_i, old_ev) in snapshot.items():
            if boundary not in found:
                del self._detected[boundary]
                deltas.append(ChangeDelta('retract', boundary, old_i, old_ev))
        for boundary, (i, ev) in found.items():
            old = self._detected.get(boundary)
            self._detected[boundary] = ev
            if old is None or not _same_evidence(old, ev):
                deltas.append(ChangeDelta('add', boundary, i, ev))
        return deltas

    # ---- 修改操作 ----

    def add(self, source, seq, value):
        key = (source, seq)
        value = Fraction(value)
        if key in self._values:
            if self._values[key] == value:
                return []  # 重复事件，不增加样本量
            return self.correct(source, seq, value)
        w = self.window
        p = len(self._order)
        snapshot = self._snapshot(p - w + 1, p + w)
        self._order.append(key)
        self._values[key] = value
        self._pos[key] = p
        return self._refresh(snapshot, p - w + 1, p + w)

    def correct(self, source, seq, value):
        key = (source, seq)
        if key not in self._values:
            raise KeyError(key)
        value = Fraction(value)
        if self._values[key] == value:
            return []
        w = self.window
        p = self._pos[key]
        snapshot = self._snapshot(p - w + 1, p + w)
        self._values[key] = value
        return self._refresh(snapshot, p - w + 1, p + w)

    def retract(self, source, seq):
        key = (source, seq)
        if key not in self._values:
            return []
        w = self.window
        p = self._pos[key]
        snapshot = self._snapshot(p - w + 1, p + w)
        del self._values[key]
        del self._pos[key]
        self._order.pop(p)
        for j in range(p, len(self._order)):
            self._pos[self._order[j]] = j
        return self._refresh(snapshot, p - w + 1, p + w)

    def apply_batch(self, ops):
        """分批输入：顺序应用一批操作，返回合并后的 ChangeDelta 列表。"""
        deltas = []
        for op in ops:
            kind = op[0]
            if kind == 'add':
                deltas.extend(self.add(*op[1:]))
            elif kind == 'correct':
                deltas.extend(self.correct(*op[1:]))
            elif kind == 'retract':
                deltas.extend(self.retract(*op[1:]))
            else:
                raise ValueError('unknown op: %r' % (kind,))
        return deltas

    # ---- 查询 ----

    def change_points(self):
        """当前全部变化点：boundary -> evidence（含当前 index）。"""
        out = {}
        for boundary, ev in self._detected.items():
            full = dict(ev)
            full['index'] = self._pos[boundary[1]]
            out[boundary] = full
        return out


class ChangePointConsumer:
    """消费方：应用 ChangeDelta 流，维护当前生效的变化点集合。"""

    def __init__(self):
        self.active = {}  # boundary -> (index, evidence)

    def apply(self, deltas):
        for d in deltas:
            if d.kind == 'add':
                self.active[d.boundary] = (d.index, d.evidence)
            elif d.kind == 'retract':
                self.active.pop(d.boundary, None)
            else:
                raise ValueError('unknown delta kind: %r' % (d.kind,))


def full_recompute(events, window, min_samples, threshold):
    """oracle：对 events=[(key, value), ...] 全量重算所有候选点。"""
    keys = [k for k, _ in events]
    values = {k: Fraction(v) for k, v in events}
    threshold = Fraction(threshold)
    n = len(keys)
    result = {}
    for i in range(n + 1):
        left = keys[max(0, i - window):i]
        right = keys[i:i + window]
        if len(left) < min_samples or len(right) < min_samples:
            continue
        lmean = sum((values[k] for k in left), Fraction(0)) / len(left)
        rmean = sum((values[k] for k in right), Fraction(0)) / len(right)
        diff = rmean - lmean
        if diff < 0:
            diff = -diff
        if diff >= threshold:
            boundary = (left[-1], right[0])
            result[boundary] = {
                'boundary': boundary,
                'index': i,
                'left': tuple(left),
                'right': tuple(right),
                'left_mean': lmean,
                'right_mean': rmean,
                'diff': diff,
            }
    return result
