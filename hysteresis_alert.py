"""风险阈值迟滞告警机（hysteresis alert machine）。

规则：
- 数值流按序号（seq）严格递增处理。
- 值 >= H 记为“高”，连续 m 次进入告警（ALERT）。
- 值 <= L 记为“低”，连续 n 次解除告警（RESOLVE），要求 L < H。
- 中间值（L < v < H）与缺测（missing）均重置高/低连续计数。
- 重复序号：值相同则幂等（无副作用），值不同则拒绝（RejectError）。
- 配置切换只影响后续数据，并重置连续计数（告警状态本身保留）。
- 状态持久化到 JSON 文件（原子写），重启后不会重复发送状态变更。

仅使用 Python 3.11 标准库。
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import asdict, dataclass, field

MISSING_TOKENS = {"NA", "N/A", "MISSING", "NONE", "NULL", ""}


class RejectError(Exception):
    """序号乱序或同序号不同值时抛出。"""


@dataclass
class Config:
    high: float
    low: float
    m: int
    n: int

    def validate(self) -> None:
        if not self.low < self.high:
            raise ValueError(f"要求 L < H，当前 L={self.low}, H={self.high}")
        if self.m < 1 or self.n < 1:
            raise ValueError(f"要求 m >= 1 且 n >= 1，当前 m={self.m}, n={self.n}")


@dataclass
class MachineState:
    config: Config
    alerted: bool = False
    high_count: int = 0
    low_count: int = 0
    last_seq: int | None = None
    last_value: float | None = None  # None 表示缺测
    events: list[dict] = field(default_factory=list)


class AlertMachine:
    """迟滞告警状态机。value 为 None 表示缺测。"""

    def __init__(self, config: Config, state: MachineState | None = None):
        config.validate()
        self.state = state if state is not None else MachineState(config=config)

    @property
    def config(self) -> Config:
        return self.state.config

    def apply_config(self, config: Config) -> None:
        """切换配置：只影响后续数据，重置连续计数，保留告警状态。"""
        config.validate()
        self.state.config = config
        self.state.high_count = 0
        self.state.low_count = 0

    def ingest(self, seq: int, value: float | None) -> dict | None:
        """处理一条数据，返回状态变更事件（无变更返回 None）。

        - seq 必须严格大于上一条；等于上一条时值相同幂等、不同拒绝。
        """
        st = self.state
        if st.last_seq is not None:
            if seq == st.last_seq:
                if value == st.last_value:
                    return None  # 幂等重放
                raise RejectError(
                    f"序号 {seq} 已处理过，值 {st.last_value!r}，拒绝不同值 {value!r}"
                )
            if seq < st.last_seq:
                raise RejectError(f"序号乱序：{seq} < 已处理的 {st.last_seq}")

        cfg = st.config
        event = None
        if value is None:
            st.high_count = 0
            st.low_count = 0
        elif value >= cfg.high:
            st.high_count += 1
            st.low_count = 0
            if not st.alerted and st.high_count >= cfg.m:
                st.alerted = True
                event = self._emit(seq, "ALERT", value)
        elif value <= cfg.low:
            st.low_count += 1
            st.high_count = 0
            if st.alerted and st.low_count >= cfg.n:
                st.alerted = False
                event = self._emit(seq, "RESOLVE", value)
        else:  # 中间值
            st.high_count = 0
            st.low_count = 0

        st.last_seq = seq
        st.last_value = value
        return event

    def _emit(self, seq: int, kind: str, value: float) -> dict:
        event = {
            "seq": seq,
            "event": kind,
            "value": value,
            "state": "ALERTED" if self.state.alerted else "NORMAL",
        }
        self.state.events.append(event)
        return event


# ---------------- 持久化 ----------------

def _state_to_dict(st: MachineState) -> dict:
    return {
        "config": asdict(st.config),
        "alerted": st.alerted,
        "high_count": st.high_count,
        "low_count": st.low_count,
        "last_seq": st.last_seq,
        "last_value": st.last_value,
        "events": st.events,
    }


def _state_from_dict(data: dict) -> MachineState:
    return MachineState(
        config=Config(**data["config"]),
        alerted=data["alerted"],
        high_count=data["high_count"],
        low_count=data["low_count"],
        last_seq=data["last_seq"],
        last_value=data["last_value"],
        events=list(data.get("events", [])),
    )


def load_machine(path: str) -> AlertMachine | None:
    """加载持久化状态；文件不存在返回 None。"""
    if not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    state = _state_from_dict(data)
    return AlertMachine(config=state.config, state=state)


def save_machine(machine: AlertMachine, path: str) -> None:
    """原子写：先写临时文件再 rename，避免崩溃留下半截状态。"""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(_state_to_dict(machine.state), f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.replace(tmp, path)


# ---------------- CLI ----------------

def _parse_value(token: str) -> float | None:
    if token.strip().upper() in MISSING_TOKENS:
        return None
    return float(token)


def _cmd_config(args: argparse.Namespace) -> int:
    cfg = Config(high=args.high, low=args.low, m=args.m, n=args.n)
    machine = load_machine(args.state)
    if machine is None:
        machine = AlertMachine(cfg)
        action = "initialized"
    else:
        machine.apply_config(cfg)
        action = "reconfigured"
    save_machine(machine, args.state)
    print(json.dumps({"status": action, "config": asdict(cfg)}, ensure_ascii=False))
    return 0


def _cmd_ingest(args: argparse.Namespace) -> int:
    machine = load_machine(args.state)
    if machine is None:
        print(f"错误：状态文件 {args.state} 不存在，请先运行 config 子命令", file=sys.stderr)
        return 2
    source = open(args.input, "r", encoding="utf-8") if args.input else sys.stdin
    rc = 0
    try:
        for lineno, raw in enumerate(source, 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split()
            if len(parts) != 2:
                print(f"第 {lineno} 行格式错误（应为 '<seq> <value|NA>'）：{line!r}",
                      file=sys.stderr)
                rc = 2
                break
            try:
                seq = int(parts[0])
                value = _parse_value(parts[1])
            except ValueError:
                print(f"第 {lineno} 行解析失败：{line!r}", file=sys.stderr)
                rc = 2
                break
            try:
                event = machine.ingest(seq, value)
            except RejectError as exc:
                print(f"第 {lineno} 行被拒绝：{exc}", file=sys.stderr)
                rc = 1
                break
            if event is not None:
                print(json.dumps(event, ensure_ascii=False))
            save_machine(machine, args.state)  # 每条落盘，崩溃不丢进度
    finally:
        if args.input:
            source.close()
    return rc


def _cmd_status(args: argparse.Namespace) -> int:
    machine = load_machine(args.state)
    if machine is None:
        print(f"错误：状态文件 {args.state} 不存在", file=sys.stderr)
        return 2
    print(json.dumps(_state_to_dict(machine.state), ensure_ascii=False, indent=2))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="hysteresis_alert",
        description="风险阈值迟滞告警机：H 连续 m 次告警，L 连续 n 次解除。",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_cfg = sub.add_parser("config", help="初始化或切换配置（重置连续计数）")
    p_cfg.add_argument("--state", required=True, help="状态文件路径（JSON）")
    p_cfg.add_argument("--high", type=float, required=True, help="高阈值 H")
    p_cfg.add_argument("--low", type=float, required=True, help="低阈值 L（须 L < H）")
    p_cfg.add_argument("--m", type=int, required=True, help="连续高出 H 多少次触发告警")
    p_cfg.add_argument("--n", type=int, required=True, help="连续低于 L 多少次解除告警")
    p_cfg.set_defaults(func=_cmd_config)

    p_ing = sub.add_parser("ingest", help="按行读取 '<seq> <value|NA>' 并处理")
    p_ing.add_argument("--state", required=True, help="状态文件路径（JSON）")
    p_ing.add_argument("input", nargs="?", help="输入文件；缺省读标准输入")
    p_ing.set_defaults(func=_cmd_ingest)

    p_st = sub.add_parser("status", help="打印当前状态（JSON）")
    p_st.add_argument("--state", required=True, help="状态文件路径（JSON）")
    p_st.set_defaults(func=_cmd_status)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
