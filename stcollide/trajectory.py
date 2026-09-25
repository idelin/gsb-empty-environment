"""分段匀速轨迹：每次速度变更在变更时刻截断并追加新段。"""

from __future__ import annotations

from bisect import bisect_right

from .core import F, vec


class Trajectory:
    """由 (段起点时刻, 段起点位置, 段速度) 序列表示的分段匀速轨迹。"""

    def __init__(self, t0, pos, vel):
        self._starts = [F(t0)]
        self._pos = [vec(pos)]
        self._vel = [vec(vel)]

    @property
    def dim(self):
        return len(self._pos[0])

    def _index(self, t):
        i = bisect_right(self._starts, t) - 1
        if i < 0:
            raise ValueError("查询时刻早于轨迹起点")
        return i

    def position(self, t):
        t = F(t)
        i = self._index(t)
        dt = t - self._starts[i]
        return tuple(p + v * dt for p, v in zip(self._pos[i], self._vel[i]))

    def velocity(self, t):
        t = F(t)
        return self._vel[self._index(t)]

    def set_velocity(self, t, vel):
        """在时刻 t 替换轨迹后缀：t 之后的运动由新速度决定。"""
        t = F(t)
        vel = vec(vel)
        pos = self.position(t)
        i = self._index(t)
        if self._starts[i] == t:
            del self._starts[i + 1 :]
            del self._pos[i + 1 :]
            del self._vel[i + 1 :]
            self._pos[i] = pos
            self._vel[i] = vel
        else:
            del self._starts[i + 1 :]
            del self._pos[i + 1 :]
            del self._vel[i + 1 :]
            self._starts.append(t)
            self._pos.append(pos)
            self._vel.append(vel)

    def breakpoints(self, t0, t1):
        """位于开区间 (t0, t1) 内的段起点列表。"""
        t0, t1 = F(t0), F(t1)
        return [s for s in self._starts if t0 < s < t1]

    def pieces(self, t0, t1):
        """把 [t0,t1] 按段切分，产出 (s, e, pos_at_s, vel)。"""
        t0, t1 = F(t0), F(t1)
        bounds = [t0] + self.breakpoints(t0, t1) + [t1]
        for s, e in zip(bounds, bounds[1:]):
            if s >= e:
                continue
            yield (s, e, self.position(s), self.velocity(s))
