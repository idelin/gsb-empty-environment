"""Discrete event simulation clock. Events are plain data (no closures)
so the whole simulation state can be snapshotted and restored."""

import heapq


class EventSim:
    def __init__(self):
        self.now = 0.0
        self._seq = 0
        self._queue = []  # (time, prio, seq, kind, payload)

    def schedule_at(self, when, kind, payload=None, prio=1):
        heapq.heappush(self._queue, (when, prio, self._seq, kind, payload))
        self._seq += 1

    def schedule(self, delay, kind, payload=None, prio=1):
        self.schedule_at(self.now + delay, kind, payload, prio)

    def peek_time(self):
        return self._queue[0][0] if self._queue else None

    def step(self, dispatch):
        when, _prio, _seq, kind, payload = heapq.heappop(self._queue)
        self.now = when
        dispatch(kind, payload)

    def to_state(self):
        return {
            "now": self.now,
            "seq": self._seq,
            "queue": [[t, p, s, k, pl] for (t, p, s, k, pl) in self._queue],
        }

    def load_state(self, state):
        self.now = state["now"]
        self._seq = state["seq"]
        self._queue = [tuple(entry) for entry in state["queue"]]
        heapq.heapify(self._queue)
