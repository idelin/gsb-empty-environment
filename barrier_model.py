"""可复用屏障（reusable barrier）的有限状态模型。

建模要点：
- 参与者可在轮间加入（join）/退出（leave），等待中可取消（cancel），
  任意成员可掉线（disconnect，掉线触发动态缩容：需求数随成员数减少）。
- 每次到达（arrive）按当前轮次打标签（atag），旧轮到达不得计入新轮。
- 轮次变更具有明确提交点：release+commit 为单个原子迁移，
  它放行本轮、递增轮次、清空本轮到达记录，并为被放行者生成放行通知。
- 上一轮遗留的放行通知（notif）可延迟送达（deliver）；
  正确模型中旧轮通知仅被丢弃，绝不能唤醒下一轮的等待者。

变体（用于演示反例搜索）：
- correct         : 正确模型。
- round_confusion : 提交点不清空到达记录，且放行不校验到达标签，
                    旧轮迟到到达会被错误计入新轮。
- stale_notif     : 旧轮延迟通知会错误唤醒新轮等待者。
"""

from __future__ import annotations

from dataclasses import dataclass

INACTIVE, ACTIVE, WAITING = 0, 1, 2

JOIN = "join"              # 轮间加入
LEAVE = "leave"            # 轮间退出（仅未等待时）
CANCEL = "cancel"          # 取消等待
DISCONNECT = "disconnect"  # 掉线（动态缩容）
ARRIVE = "arrive"          # 到达屏障（按当前轮次打标签）
RELEASE = "release"        # 放行并提交新一轮（原子提交点）
DELIVER = "deliver"        # 送达遗留的放行通知

PARTICIPANT_ACTIONS = (JOIN, LEAVE, CANCEL, DISCONNECT, ARRIVE, DELIVER)

VARIANTS = ("correct", "round_confusion", "stale_notif")


@dataclass(frozen=True)
class State:
    rnd: int            # 当前轮次，仅由 release+commit 递增
    released_upto: int  # 已放行的最高轮次（供 P3 监控）
    status: tuple       # 每个参与者 INACTIVE/ACTIVE/WAITING
    notif: tuple        # 每个参与者待送达通知所属轮次，-1 表示无
    atag: tuple         # 每个参与者当前到达记录所属轮次，-1 表示未到达


@dataclass(frozen=True)
class Model:
    n: int = 2               # 参与者数（有限宇宙）
    max_round: int = 3       # 轮次上限，保证模型有限
    variant: str = "correct"

    def initial_state(self) -> State:
        return State(0, -1, (ACTIVE,) * self.n, (-1,) * self.n, (-1,) * self.n)


def _set(tup, i, v):
    lst = list(tup)
    lst[i] = v
    return tuple(lst)


def members(model: Model, s: State):
    return [p for p in range(model.n) if s.status[p] != INACTIVE]


def _release_enabled(model: Model, s: State) -> bool:
    if s.rnd >= model.max_round:
        return False
    memb = members(model, s)
    if not memb:
        return False
    if model.variant == "round_confusion":
        # 缺陷：只数到达个数，不校验到达是否属于当前轮。
        return sum(1 for p in range(model.n) if s.atag[p] != -1) >= len(memb)
    # 正确：全体成员必须在当前轮到达（等待中）。
    return all(s.status[p] == WAITING for p in memb)


def enabled_actions(model: Model, s: State):
    acts = []
    for p in range(model.n):
        st = s.status[p]
        if st == INACTIVE:
            acts.append((JOIN, p))
        else:
            acts.append((DISCONNECT, p))
            if st == ACTIVE:
                acts.append((LEAVE, p))
                acts.append((ARRIVE, p))
            else:  # WAITING
                acts.append((CANCEL, p))
        if s.notif[p] != -1:
            acts.append((DELIVER, p))
    if _release_enabled(model, s):
        acts.append((RELEASE,))
    return acts


def step(model: Model, s: State, a) -> State:
    kind = a[0]
    if kind == RELEASE:
        status, notif, atag = list(s.status), list(s.notif), list(s.atag)
        for p in range(model.n):
            if s.atag[p] != -1:
                notif[p] = s.rnd          # 留下本轮放行通知
                status[p] = ACTIVE
                if model.variant != "round_confusion":
                    atag[p] = -1          # 提交点清空本轮到达记录
        return State(s.rnd + 1, s.rnd, tuple(status), tuple(notif), tuple(atag))
    p = a[1]
    if kind == JOIN:
        return State(s.rnd, s.released_upto, _set(s.status, p, ACTIVE), s.notif, s.atag)
    if kind == LEAVE:
        return State(s.rnd, s.released_upto, _set(s.status, p, INACTIVE), s.notif, s.atag)
    if kind == DISCONNECT:
        # 掉线：退出成员、清除到达与通知，需求数随成员数缩容。
        return State(s.rnd, s.released_upto, _set(s.status, p, INACTIVE),
                     _set(s.notif, p, -1), _set(s.atag, p, -1))
    if kind == ARRIVE:
        return State(s.rnd, s.released_upto, _set(s.status, p, WAITING),
                     s.notif, _set(s.atag, p, s.rnd))
    if kind == CANCEL:
        return State(s.rnd, s.released_upto, _set(s.status, p, ACTIVE),
                     s.notif, _set(s.atag, p, -1))
    if kind == DELIVER:
        if model.variant == "stale_notif" and s.status[p] == WAITING:
            # 缺陷：旧轮延迟通知唤醒了新轮等待者。
            return State(s.rnd, s.released_upto, _set(s.status, p, ACTIVE),
                         _set(s.notif, p, -1), _set(s.atag, p, -1))
        # 正确：通知只是信息性的，送达即清除，绝不影响等待状态。
        return State(s.rnd, s.released_upto, s.status, _set(s.notif, p, -1), s.atag)
    raise ValueError(f"unknown action {a}")


def transitions(model: Model, s: State):
    return [(a, step(model, s, a)) for a in enabled_actions(model, s)]


def violations(model: Model, s: State, a, s2: State):
    """迁移级安全监控器，返回被违反的性质名列表。"""
    bad = []
    if a[0] == RELEASE:
        memb = members(model, s)
        if any(s.atag[p] == -1 for p in memb):
            bad.append("P1_premature_release")   # 同轮未齐却放行
        if any(0 <= s.atag[p] < s.rnd for p in memb):
            bad.append("P2_stale_arrival")       # 旧轮到达计入新轮
        if s.rnd <= s.released_upto:
            bad.append("P3_double_release")      # 同一轮放行超过一次
    elif a[0] == DELIVER:
        p = a[1]
        if (s.status[p] == WAITING and 0 <= s.notif[p] < s.rnd
                and s2.status[p] != WAITING):
            bad.append("P4_stale_notify")        # 旧轮延迟通知唤醒新轮等待者
    return bad


def canon(s: State):
    """对称归约的规范化：参与者不可区分，按参与者三元组排序。"""
    return (s.rnd, s.released_upto, tuple(sorted(zip(s.status, s.notif, s.atag))))


def fmt_action(a) -> str:
    if a[0] == RELEASE:
        return "release+commit"
    return f"{a[0]}(P{a[1]})"


def fmt_trace(trace) -> str:
    return " -> ".join(fmt_action(a) for a in trace) if trace else "(empty)"
