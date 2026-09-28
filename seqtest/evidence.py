"""可重复查询、可 checkpoint 的证据过程。

证据状态保存 (n, k) 与对数累计证据 log_lr，三者一起序列化；
恢复时用充分统计量重算的闭式对数似然比做一致性校验。
判定一律使用累计值对原始边界做 >= / <=，不引入任何 epsilon。
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .config import SprtConfig


class Decision:
    CONTINUE = "CONTINUE"          # 继续抽样
    ACCEPT_H1 = "ACCEPT_H1"        # 接受 H1（拒绝 H0）
    ACCEPT_H0 = "ACCEPT_H0"        # 接受 H0（拒绝 H1）
    INCONCLUSIVE = "INCONCLUSIVE"  # 到达最大样本数仍未决

    @staticmethod
    def is_terminal(d: str) -> bool:
        return d != Decision.CONTINUE


@dataclass
class Evidence:
    config: SprtConfig
    n: int = 0
    k: int = 0
    log_lr: float = 0.0

    @classmethod
    def fresh(cls, config: SprtConfig) -> "Evidence":
        return cls(config=config, n=0, k=0, log_lr=0.0)

    def add_observation(self, x: int) -> str:
        """并入一个伯努利观测并返回当前判定；终止后再并入会抛错。"""
        if Decision.is_terminal(self.decision()):
            raise RuntimeError("证据过程已终止，不能再并入观测")
        self.n += 1
        self.k += int(x)
        # 以整数计数累计式为准，与精确递推逐位一致（不做逐次浮点和、不引 epsilon）。
        self.log_lr = self.config.canonical_log_lr(self.n, self.k)
        return self.decision()

    def decision(self) -> str:
        """当前判定（可重复查询，不改变状态，不用 epsilon）。"""
        # 边界为闭区间：命中即停，优先于 n_max 检查。
        if self.log_lr >= self.config.log_a:
            return Decision.ACCEPT_H1
        if self.log_lr <= self.config.log_b:
            return Decision.ACCEPT_H0
        if self.n >= self.config.n_max:
            return Decision.INCONCLUSIVE
        return Decision.CONTINUE

    def canonical_log_lr(self) -> float:
        return self.config.canonical_log_lr(self.n, self.k)

    def snapshot(self) -> dict:
        """完整 checkpoint（含配置），JSON 可序列化（inf 用 null 兜底）。"""
        return {
            "config": self.config.to_dict(),
            "n": self.n,
            "k": self.k,
            "log_lr": _json_float(self.log_lr),
        }

    @classmethod
    def from_snapshot(cls, snap: dict, *, check_consistency: bool = True) -> "Evidence":
        cfg = SprtConfig.from_dict(snap["config"])
        ev = cls(config=cfg, n=int(snap["n"]), k=int(snap["k"]),
                 log_lr=_float_from_json(snap["log_lr"]))
        if check_consistency and ev.n > 0:
            canon = ev.canonical_log_lr()
            if not math.isclose(ev.log_lr, canon, rel_tol=1e-12, abs_tol=1e-12) and not (
                math.isinf(ev.log_lr) and math.isinf(canon) and ev.log_lr == canon
            ):
                raise ValueError("checkpoint 与充分统计量重算结果不一致")
        return ev

    @classmethod
    def replay(cls, config: SprtConfig, observations) -> "Evidence":
        """由观测序列确定性重放（恢复一致；不经过随机源）。"""
        ev = cls.fresh(config)
        for x in observations:
            if Decision.is_terminal(ev.add_observation(x)):
                break
        return ev


_INF_TOKENS = {"Infinity": math.inf, "-Infinity": -math.inf, "NaN": math.nan}


def _json_float(v: float):
    """inf/-inf/nan 用字符串哨兵表示，保证标准 JSON 可往返。"""
    if math.isinf(v):
        return "Infinity" if v > 0 else "-Infinity"
    if math.isnan(v):
        return "NaN"
    return v


def _float_from_json(v):
    if isinstance(v, str):
        return _INF_TOKENS[v]
    return float(v)
