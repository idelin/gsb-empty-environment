#!/usr/bin/env python3
"""按主体撤回的聚合物化视图(纯 Python 3.11 标准库实现)。

数据模型
--------
事件(Event)字段:
  subject             主体标识(字符串)
  event_id            事件 ID(字符串,全局唯一标识一次贡献)
  group               分组(字符串)
  value               整数值
  revision            修订号(非负整数)
  consent_generation  同意代次(非负整数)

语义
----
1. 修订替换:同一 event_id 仅保留最高 revision 的贡献,旧贡献被替换;
   不高于已存 revision 的重放被拒绝。
2. 主体撤回:revoke 删除该主体当前全部贡献并留下墓碑(tombstone),
   墓碑记录撤回时主体的最高同意代次 G;此后 consent_generation <= G
   的事件一律拒绝,较旧事件无法复活已删除的贡献。
3. 重新同意:主体必须携带更高同意代次(> G)重新贡献,方被接受。
4. 查询:支持按分组及全局的 sum / count / 精确中位数(偶数个取中间两值
   的算术平均,内部用 Fraction 保证精确)。
5. 对照:verify() 将增量维护的结果与从操作日志全量重算的结果逐一比对。
6. 持久化:状态以 JSON 操作日志原子写盘,重启后重放恢复。
"""
from __future__ import annotations

import argparse
import bisect
import dataclasses
import json
import os
import sys
import tempfile
from dataclasses import dataclass
from fractions import Fraction

GLOBAL = None  # 内部分组键:None 表示全局聚合


@dataclass(frozen=True)
class Event:
    subject: str
    event_id: str
    group: str
    value: int
    revision: int
    consent_generation: int

    @staticmethod
    def from_dict(data: dict) -> "Event":
        try:
            event = Event(
                subject=str(data["subject"]),
                event_id=str(data["event_id"]),
                group=str(data["group"]),
                value=data["value"],
                revision=data["revision"],
                consent_generation=data["consent_generation"],
            )
        except KeyError as exc:
            raise ValueError(f"事件缺少字段: {exc}") from exc
        for name in ("value", "revision", "consent_generation"):
            field_value = getattr(event, name)
            if isinstance(field_value, bool) or not isinstance(field_value, int):
                raise ValueError(f"字段 {name} 必须为整数,得到 {field_value!r}")
        if event.revision < 0 or event.consent_generation < 0:
            raise ValueError("revision 与 consent_generation 必须为非负整数")
        return event


def _median_of(sorted_values: list[int]):
    """精确中位数:奇数取中位,偶数取中间两值的算术平均(Fraction)。"""
    count = len(sorted_values)
    if count == 0:
        return None
    mid = count // 2
    if count % 2:
        return Fraction(sorted_values[mid])
    return Fraction(sorted_values[mid - 1] + sorted_values[mid], 2)


def median_to_json(median):
    """Fraction -> JSON 可序列化:整值输出 int,否则输出 float。"""
    if median is None:
        return None
    if median.denominator == 1:
        return median.numerator
    return median.numerator / median.denominator


class MaterializedView:
    """增量维护的聚合物化视图,支持主体撤回与代次控制。"""

    FORMAT = "subject-revocable-view/1"

    def __init__(self) -> None:
        self._events: dict[str, Event] = {}       # event_id -> 当前生效的最新修订
        self._tombstones: dict[str, int] = {}     # subject -> 撤回时的同意代次
        self._subject_gen: dict[str, int] = {}    # subject -> 已接受的最高同意代次
        self._log: list[dict] = []                # 已应用操作日志(持久化/全量重算用)
        self._sum: dict = {GLOBAL: 0}             # 分组 -> 总和
        self._values: dict = {GLOBAL: []}         # 分组 -> 升序值列表(精确中位数用)

    # ---- 内部增量维护 ----

    def _add(self, event: Event) -> None:
        for key in (GLOBAL, event.group):
            self._sum[key] = self._sum.get(key, 0) + event.value
            bisect.insort(self._values.setdefault(key, []), event.value)

    def _remove(self, event: Event) -> None:
        for key in (GLOBAL, event.group):
            self._sum[key] -= event.value
            values = self._values[key]
            del values[bisect.bisect_left(values, event.value)]

    # ---- 写操作 ----

    def apply_event(self, event: Event, record: bool = True) -> bool:
        """应用事件;返回是否被接受(被拒绝的过期/墓碑事件无副作用)。"""
        tombstone = self._tombstones.get(event.subject)
        if tombstone is not None and event.consent_generation <= tombstone:
            return False  # 墓碑:较旧或同代次事件不得复活
        if event.consent_generation < self._subject_gen.get(event.subject, 0):
            return False  # 过期代次
        existing = self._events.get(event.event_id)
        if existing is not None:
            if event.revision <= existing.revision:
                return False  # 过期修订
            self._remove(existing)
        self._subject_gen[event.subject] = max(
            event.consent_generation, self._subject_gen.get(event.subject, 0)
        )
        self._events[event.event_id] = event
        self._add(event)
        if record:
            self._log.append({"op": "event", **dataclasses.asdict(event)})
        return True

    def revoke(self, subject: str, record: bool = True) -> bool:
        """撤回主体:删除其全部贡献并留下墓碑。"""
        generation = self._subject_gen.get(subject, 0)
        tombstone = self._tombstones.get(subject)
        if tombstone is None or generation > tombstone:
            self._tombstones[subject] = generation
        for event_id in [eid for eid, ev in self._events.items() if ev.subject == subject]:
            self._remove(self._events.pop(event_id))
        if record:
            self._log.append({"op": "revoke", "subject": subject})
        return True

    def apply_op(self, op: dict, record: bool = True) -> bool:
        kind = op.get("op")
        if kind == "event":
            return self.apply_event(Event.from_dict(op), record=record)
        if kind == "revoke":
            subject = op.get("subject")
            if not isinstance(subject, str) or not subject:
                raise ValueError(f"revoke 操作缺少合法 subject: {op!r}")
            return self.revoke(subject, record=record)
        raise ValueError(f"未知操作类型: {kind!r}")

    # ---- 查询 ----

    def query(self, group=GLOBAL) -> dict:
        """返回 {"group", "sum", "count", "median"};median 为 Fraction/None。"""
        values = self._values.get(group, [])
        return {
            "group": group,
            "sum": self._sum.get(group, 0),
            "count": len(values),
            "median": _median_of(values),
        }

    def groups(self) -> list:
        return sorted(key for key in self._values if key is not GLOBAL)

    # ---- 增量 vs 全量重算对照 ----

    def _snapshot(self) -> dict:
        return {
            group: (total, tuple(self._values.get(group, ())))
            for group, total in self._sum.items()
        }

    def full_recompute(self) -> "MaterializedView":
        """从操作日志全量重算一个全新视图。"""
        fresh = MaterializedView()
        for op in self._log:
            fresh.apply_op(op, record=False)
        return fresh

    def verify(self) -> bool:
        """增量维护结果与全量重算结果一致时返回 True。"""
        return self._snapshot() == self.full_recompute()._snapshot()

    # ---- 持久化 ----

    def save(self, path: str) -> None:
        payload = {"format": self.FORMAT, "ops": self._log}
        directory = os.path.dirname(os.path.abspath(path))
        fd, tmp_path = tempfile.mkstemp(dir=directory, suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                json.dump(payload, handle, ensure_ascii=False)
            os.replace(tmp_path, path)  # 原子替换,避免半截文件
        except BaseException:
            if os.path.exists(tmp_path):
                os.unlink(tmp_path)
            raise

    @classmethod
    def load(cls, path: str) -> "MaterializedView":
        with open(path, encoding="utf-8") as handle:
            payload = json.load(handle)
        if payload.get("format") != cls.FORMAT:
            raise ValueError(f"无法识别的状态文件格式: {payload.get('format')!r}")
        view = cls()
        for op in payload["ops"]:
            view.apply_op(op, record=True)
        return view


# ---- 命令行接口 ----


def _cmd_process(view: MaterializedView, args) -> int:
    source = sys.stdin if args.ops in (None, "-") else open(args.ops, encoding="utf-8")
    try:
        for lineno, line in enumerate(source, 1):
            line = line.strip()
            if not line:
                continue
            try:
                accepted = view.apply_op(json.loads(line))
            except (ValueError, KeyError, TypeError) as exc:
                print(json.dumps({"line": lineno, "error": str(exc)},
                                 ensure_ascii=False))
                return 2
            print(json.dumps({"line": lineno, "accepted": accepted},
                             ensure_ascii=False))
    finally:
        if source is not sys.stdin:
            source.close()
    return 0


def _cmd_query(view: MaterializedView, args) -> int:
    result = view.query(group=args.group)
    result["median"] = median_to_json(result["median"])
    if result["group"] is GLOBAL:
        result["group"] = "global"
    print(json.dumps(result, ensure_ascii=False))
    return 0


def _cmd_verify(view: MaterializedView, args) -> int:
    consistent = view.verify()
    print(json.dumps({"consistent": consistent}, ensure_ascii=False))
    return 0 if consistent else 1


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="materialized_view",
        description="按主体撤回的聚合物化视图(增量聚合 / 全量重算对照 / 持久化)",
    )
    parser.add_argument("--db", help="持久化状态文件(JSON);存在则加载,命令结束后保存")
    subparsers = parser.add_subparsers(dest="command", required=True)

    proc = subparsers.add_parser("process", help="逐行处理 JSONL 操作流")
    proc.add_argument("ops", nargs="?", default="-",
                      help="JSONL 操作文件路径,省略或 '-' 表示标准输入")
    proc.set_defaults(func=_cmd_process)

    query = subparsers.add_parser("query", help="查询聚合结果")
    query.add_argument("--group", default=None, help="分组名;省略表示全局")
    query.set_defaults(func=_cmd_query)

    verify = subparsers.add_parser("verify", help="增量结果与全量重算对照")
    verify.set_defaults(func=_cmd_verify)

    args = parser.parse_args(argv)
    view = MaterializedView()
    if args.db and os.path.exists(args.db):
        view = MaterializedView.load(args.db)
    exit_code = args.func(view, args)
    if args.db and exit_code == 0:
        view.save(args.db)
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
