"""有限状态模型定义。

验证对象仅限于此处显式给出的有限状态模型：
状态集合、初始状态、带动作标签的迁移，以及每个状态上的原子命题。
检查器不会在模型之外构造任何额外行为。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Transition:
    """一条带动作标签的迁移。"""

    src: str
    action: str
    dst: str

    def __str__(self) -> str:  # pragma: no cover - 仅用于展示
        return f"{self.src} --{self.action}--> {self.dst}"


class Model:
    """显式枚举的有限状态迁移系统。"""

    def __init__(self, name, initial, transitions, labels):
        self.name = name
        self.initial = initial
        self.transitions = tuple(transitions)
        self.labels = {s: frozenset(props) for s, props in labels.items()}

        states = {initial} | set(self.labels)
        for t in self.transitions:
            states.add(t.src)
            states.add(t.dst)
        self.states = frozenset(states)

        self._out = {s: [] for s in self.states}
        for t in self.transitions:
            self._out[t.src].append(t)
        self._transition_set = set(self.transitions)

    def outgoing(self, state):
        return self._out.get(state, [])

    def enabled_actions(self, state):
        """该状态下可执行的动作集合（用于公平性判定）。"""
        return {t.action for t in self.outgoing(state)}

    def props(self, state):
        return self.labels.get(state, frozenset())

    def has_transition(self, transition):
        return transition in self._transition_set
