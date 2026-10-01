"""用户旅程去重归因核心引擎（纯内存，可由持久日志重放重建）。

归因规则
--------
对每笔成交，取业务时间落在 ``[成交时间 - 窗口, 成交时间)`` 内、且与成交主体
处于同一身份连通分量的最后一个渠道触点；业务时间相同取触点事件 ID 最小者。
窗口左边界为闭区间（触点时间 == 成交时间 - 窗口 时纳入归因），
右边界为开区间（触点必须严格早于成交）。

事件修订按版本覆盖：同一 event_id 仅保留 version 最大的记录，低版本到达被忽略。
身份边为带版本的无向边：edge_add / edge_remove 按 edge_id 取最大 version 生效，
边删除可导致主体连通分量分裂。
"""
from __future__ import annotations

TOUCHPOINT = "touchpoint"
CONVERSION = "conversion"
EDGE_ADD = "edge_add"
EDGE_REMOVE = "edge_remove"

EVENT_OPS = (TOUCHPOINT, CONVERSION)
EDGE_OPS = (EDGE_ADD, EDGE_REMOVE)

#: 守恒明细中“未归因”伪渠道的键名（JSON 对象键不能为 null）
UNATTRIBUTED_KEY = "<unattributed>"


class UnionFind:
    def __init__(self):
        self.parent = {}

    def find(self, x):
        self.parent.setdefault(x, x)
        root = x
        while self.parent[root] != root:
            root = self.parent[root]
        node = x
        while self.parent[node] != root:
            self.parent[node], node = root, self.parent[node]
        return root

    def union(self, a, b):
        ra, rb = self.find(a), self.find(b)
        if ra != rb:
            self.parent[rb] = ra


def _require_int(value, field):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{field} 必须为整数, got {value!r}")
    return value


def validate_op(op):
    """校验操作格式；金额与业务时间强制为整数。"""
    if not isinstance(op, dict):
        raise ValueError("op 必须为 JSON 对象")
    kind = op.get("op")
    if kind not in EVENT_OPS + EDGE_OPS:
        raise ValueError(f"未知 op 类型: {kind!r}")
    if not isinstance(op.get("idempotency_key"), str) or not op["idempotency_key"]:
        raise ValueError("idempotency_key 必须为非空字符串")
    _require_int(op.get("version"), "version")
    if op["version"] < 1:
        raise ValueError("version 必须 >= 1")
    if kind in EVENT_OPS:
        for field in ("event_id", "subject"):
            if not isinstance(op.get(field), str) or not op[field]:
                raise ValueError(f"{field} 必须为非空字符串")
        _require_int(op.get("time"), "time")
        if kind == TOUCHPOINT:
            if not isinstance(op.get("channel"), str) or not op["channel"]:
                raise ValueError("channel 必须为非空字符串")
        else:
            _require_int(op.get("amount"), "amount")
    else:
        if not isinstance(op.get("edge_id"), str) or not op["edge_id"]:
            raise ValueError("edge_id 必须为非空字符串")
        for field in ("u", "v"):
            if not isinstance(op.get(field), str) or not op[field]:
                raise ValueError(f"{field} 必须为非空字符串")
        if op["u"] == op["v"]:
            raise ValueError("身份边两端不能相同")


class State:
    """事件与身份图的当前状态（修订已按版本覆盖）。"""

    def __init__(self, window):
        self.window = _require_int(window, "window")
        if self.window < 0:
            raise ValueError("window 必须 >= 0")
        self.events = {}  # event_id -> {version, kind, subject, time, channel|amount}
        self.edges = {}   # edge_id -> {version, u, v, active}


def build_union_find(state):
    uf = UnionFind()
    for ev in state.events.values():
        uf.find(ev["subject"])
    for edge in state.edges.values():
        uf.find(edge["u"])
        uf.find(edge["v"])
        if edge["active"]:
            uf.union(edge["u"], edge["v"])
    return uf


def attribute_conversion(state, conv_id, uf=None):
    """计算单笔成交的归因，返回 {"touchpoint_id", "channel"} 或 None。"""
    conv = state.events[conv_id]
    if uf is None:
        uf = build_union_find(state)
    root = uf.find(conv["subject"])
    best = None  # (time, event_id, channel)
    for eid, ev in state.events.items():
        if ev["kind"] != TOUCHPOINT:
            continue
        if uf.find(ev["subject"]) != root:
            continue
        if not (conv["time"] - state.window <= ev["time"] < conv["time"]):
            continue
        if best is None or ev["time"] > best[0] or (ev["time"] == best[0] and eid < best[1]):
            best = (ev["time"], eid, ev["channel"])
    if best is None:
        return None
    return {"touchpoint_id": best[1], "channel": best[2]}


def full_recompute(state):
    """全量重算：独立对照实现，返回 {conversion_id: attribution|None}。"""
    uf = build_union_find(state)
    return {
        eid: attribute_conversion(state, eid, uf=uf)
        for eid, ev in state.events.items()
        if ev["kind"] == CONVERSION
    }


def make_change(conv, old, new):
    if old is None:
        kind = "attribute"    # 新增归因
    elif new is None:
        kind = "revoke"       # 撤销归因
    else:
        kind = "reattribute"  # 改归渠道
    return {
        "conversion_id": conv["event_id"],
        "kind": kind,
        "old_touchpoint": old["touchpoint_id"] if old else None,
        "new_touchpoint": new["touchpoint_id"] if new else None,
        "old_channel": old["channel"] if old else None,
        "new_channel": new["channel"] if new else None,
        "amount": conv["amount"],
    }


def summarize(changes):
    """金额守恒明细：把“未归因”视为伪渠道，则每笔受影响成交的金额在
    重算前后各被恰好计入一次，before/after 总额必然相等（conserved=True），
    全部渠道（含未归因）差额之和为 0。"""
    before, after = {}, {}
    for ch in changes:
        if ch["kind"] == "amount":
            continue  # 纯金额修订不属于渠道间金额移动
        old_key = ch["old_channel"] if ch["old_channel"] is not None else UNATTRIBUTED_KEY
        new_key = ch["new_channel"] if ch["new_channel"] is not None else UNATTRIBUTED_KEY
        before[old_key] = before.get(old_key, 0) + ch["amount"]
        after[new_key] = after.get(new_key, 0) + ch["amount"]
    return {
        "before": before,
        "after": after,
        "conserved": sum(before.values()) == sum(after.values()),
    }


class Engine:
    """增量归因引擎：每次操作只精准重算受影响的成交。"""

    def __init__(self, window):
        self.state = State(window)
        self.attributions = {}  # conversion_id -> attribution|None

    # -- 受影响成交集合 -------------------------------------------------

    def _touch_affected(self, tp):
        """触点 tp 可能影响的成交：同一分量且 tp.time < c.time <= tp.time + window。"""
        uf = build_union_find(self.state)
        root = uf.find(tp["subject"])
        lo, hi = tp["time"], tp["time"] + self.state.window
        return {
            eid
            for eid, ev in self.state.events.items()
            if ev["kind"] == CONVERSION
            and lo < ev["time"] <= hi
            and uf.find(ev["subject"]) == root
        }

    # -- 重算并生成变化清单 ---------------------------------------------

    def recompute(self, conv_ids):
        changes = []
        if not conv_ids:
            return changes
        uf = build_union_find(self.state)
        for cid in sorted(conv_ids):
            conv = self.state.events.get(cid)
            if conv is None or conv["kind"] != CONVERSION:
                continue
            old = self.attributions.get(cid)
            new = attribute_conversion(self.state, cid, uf=uf)
            if old != new:
                self.attributions[cid] = new
                changes.append(make_change(conv, old, new))
        return changes

    # -- 操作入口 --------------------------------------------------------

    def apply(self, op):
        validate_op(op)
        if op["op"] in EVENT_OPS:
            return self._apply_event(op)
        return self._apply_edge(op)

    def _apply_event(self, op):
        eid = op["event_id"]
        old = self.state.events.get(eid)
        if old is not None:
            if old["kind"] != op["op"]:
                raise ValueError(f"事件 {eid!r} 的类型不可变更")
            if op["version"] <= old["version"]:
                return {"applied": False, "changes": [], "conservation": summarize([])}
        ev = {
            "event_id": eid,
            "version": op["version"],
            "kind": op["op"],
            "subject": op["subject"],
            "time": op["time"],
        }
        if op["op"] == TOUCHPOINT:
            ev["channel"] = op["channel"]
        else:
            ev["amount"] = op["amount"]

        affected = set()
        if ev["kind"] == TOUCHPOINT:
            # 迟到触点：只重算窗口覆盖该触点的成交；修订时旧版本影响的成交也要重算
            affected |= self._touch_affected(ev)
            if old is not None:
                affected |= self._touch_affected(old)
        else:
            affected.add(eid)  # 成交（含修订）只影响自身
        self.state.events[eid] = ev
        changes = self.recompute(affected)

        # 纯金额修订（归因渠道不变）也输出，便于对账；不计入渠道守恒明细
        if (
            ev["kind"] == CONVERSION
            and old is not None
            and old["amount"] != ev["amount"]
            and all(c["conversion_id"] != eid for c in changes)
        ):
            attr = self.attributions.get(eid)
            changes.append({
                "conversion_id": eid,
                "kind": "amount",
                "old_touchpoint": attr["touchpoint_id"] if attr else None,
                "new_touchpoint": attr["touchpoint_id"] if attr else None,
                "old_channel": attr["channel"] if attr else None,
                "new_channel": attr["channel"] if attr else None,
                "old_amount": old["amount"],
                "new_amount": ev["amount"],
                "amount": ev["amount"],
            })
        return {"applied": True, "changes": changes, "conservation": summarize(changes)}

    def _apply_edge(self, op):
        eid = op["edge_id"]
        old = self.state.edges.get(eid)
        if old is not None and op["version"] <= old["version"]:
            return {"applied": False, "changes": [], "conservation": summarize([])}
        # 身份变更：精准重算变更前连通分量内的全部成交
        # （合并 = 两分量并集；拆分 = 原分量，二者均由此集合覆盖）
        uf_before = build_union_find(self.state)
        roots = {uf_before.find(op["u"]), uf_before.find(op["v"])}
        affected = {
            eid2
            for eid2, ev in self.state.events.items()
            if ev["kind"] == CONVERSION and uf_before.find(ev["subject"]) in roots
        }
        self.state.edges[eid] = {
            "edge_id": eid,
            "version": op["version"],
            "u": op["u"],
            "v": op["v"],
            "active": op["op"] == EDGE_ADD,
        }
        changes = self.recompute(affected)
        return {"applied": True, "changes": changes, "conservation": summarize(changes)}
