"""多方求和协议模拟：参与集合承诺、分享、掉线确认、恢复与聚合。

重要声明：
- 本模块仅模拟协议流程与有限域守恒性质，不作密码学安全背书，
  不是生产级密码协议实现。
- 串谋抵抗、消息认证与身份真实性不在本模拟的保证范围内
  （份额对模拟协调者可见，消息无 MAC/签名）。
- 仅处理合成数据。

轮次语义：
- 每轮输出要么为 COMPLETE 且明确标注覆盖集合（活跃方），
  要么为 INCOMPLETE 且不携带任何数值；
  阈值不足或缺少必要份额/恢复材料时，绝不输出部分和冒充总和。
- 恢复阶段恢复的是"因持有方掉线而丢失的份额对总和的贡献"
  （由活跃拥有方释放其发往掉线方的份额），而非掉线方的输入；
  掉线方的输入被排除在覆盖集合之外。这是加法分享的固有性质：
  掉线方自身持有的份额随掉线丢失，其秘密无法被完整重构。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

from .sharing import DEFAULT_PRIME, Share, split_secret


class Phase(Enum):
    COMMIT = "参与集合承诺"
    SHARE = "分享"
    CONFIRM_DROPOUT = "掉线确认"
    RECOVERY = "恢复"
    AGGREGATE = "聚合"
    CLOSED = "已关闭"


class RoundStatus(Enum):
    COMPLETE = "完成"
    INCOMPLETE = "未完成"


@dataclass(frozen=True)
class RoundResult:
    """轮次结果。INCOMPLETE 时 value 与 covered_parties 均为 None。"""

    round_id: str
    status: RoundStatus
    value: int | None
    covered_parties: tuple[str, ...] | None
    reason: str | None = None


@dataclass
class Party:
    party_id: str
    input_value: int
    inbox: list = field(default_factory=list)
    outbox: list = field(default_factory=list)


class RejectionError(Exception):
    """协议拒绝：旧轮份额、重复重传、越权恢复、阶段违规等。"""


class Round:
    """单轮多方求和。Round 对象充当模拟的公告板/协调者。"""

    def __init__(self, round_id, threshold, prime=DEFAULT_PRIME):
        if threshold < 2:
            raise ValueError("阈值至少为 2")
        self.round_id = round_id
        self.threshold = threshold
        self.prime = prime
        self.phase = Phase.COMMIT
        self.parties: dict[str, Party] = {}
        self.committed: tuple[str, ...] = ()
        self.dropped: frozenset[str] = frozenset()
        self.shares: dict[tuple[str, str], Share] = {}
        self.recovery_material: dict[tuple[str, str], int] = {}
        self.partials: dict[str, int] = {}
        self.rejections: list[str] = []
        self.result: RoundResult | None = None

    def _require_phase(self, phase):
        if self.phase is not phase:
            raise RejectionError(
                f"当前阶段为 {self.phase.value}，拒绝执行 {phase.value} 阶段操作"
            )

    def _reject(self, message):
        self.rejections.append(message)
        raise RejectionError(message)

    # ---- 阶段一：参与集合承诺 ----

    def commit(self, party):
        if self.phase is not Phase.COMMIT:
            self._reject("承诺阶段已关闭，拒绝加入")
        if party.party_id in self.parties:
            self._reject(f"参与方 {party.party_id!r} 重复承诺")
        self.parties[party.party_id] = party

    def close_commitment(self):
        self._require_phase(Phase.COMMIT)
        if len(self.parties) < self.threshold:
            self._reject("承诺方数量低于阈值，无法开轮")
        self.committed = tuple(self.parties)
        self.phase = Phase.SHARE

    # ---- 阶段二：分享 ----

    def create_shares(self, owner_id, rng=None):
        """拥有者为自己的输入生成面向全部承诺方的份额。"""
        self._require_phase(Phase.SHARE)
        if owner_id not in self.committed:
            self._reject("未承诺方不能分享")
        party = self.parties[owner_id]
        shares = split_secret(
            party.input_value,
            len(self.committed),
            self.prime,
            self.round_id,
            owner_id,
            self.committed,
            rng,
        )
        party.outbox.extend(shares)
        return shares

    def deliver_share(self, sender_id, share):
        """投递单份份额；旧轮份额与重复重传一律拒绝。"""
        if self.phase is not Phase.SHARE:
            self._reject("分享阶段已关闭，拒绝迟到的份额")
        if share.round_id != self.round_id:
            self._reject(
                f"轮次隔离：份额属于轮次 {share.round_id!r}，"
                f"拒绝混入轮次 {self.round_id!r}"
            )
        if share.owner != sender_id:
            self._reject("只能投递自己的份额")
        if share.owner not in self.committed or share.holder not in self.committed:
            self._reject("份额涉及未承诺方")
        if not 0 <= share.value < self.prime:
            self._reject("份额取值超出有限域")
        key = (share.owner, share.holder)
        if key in self.shares:
            self._reject(f"重复重传 ({share.owner!r} -> {share.holder!r})，拒绝")
        self.shares[key] = share
        self.parties[share.holder].inbox.append(share)
        return True

    def close_sharing(self):
        self._require_phase(Phase.SHARE)
        missing = [
            (o, h)
            for o in self.committed
            for h in self.committed
            if (o, h) not in self.shares
        ]
        if missing:
            self._reject(f"缺少必要份额，无法进入下一阶段: {missing}")
        self.phase = Phase.CONFIRM_DROPOUT

    # ---- 阶段三：掉线确认 ----

    def confirm_dropouts(self, dropped_ids):
        self._require_phase(Phase.CONFIRM_DROPOUT)
        dropped = frozenset(dropped_ids)
        unknown = dropped - set(self.committed)
        if unknown:
            self._reject(f"掉线声明包含未承诺方: {sorted(unknown)}")
        self.dropped = dropped
        self.phase = Phase.RECOVERY

    @property
    def active(self):
        """活跃方 = 承诺方 - 已确认掉线方。"""
        return tuple(p for p in self.committed if p not in self.dropped)

    # ---- 阶段四：恢复 ----

    def release_recovery_material(self, owner_id, holder_id):
        """活跃拥有方释放其发往已确认掉线持有方的份额（恢复材料）。

        恢复材料只能用于声明掉线方；指向未掉线方的请求一律拒绝。
        """
        self._require_phase(Phase.RECOVERY)
        if holder_id not in self.dropped:
            self._reject("恢复材料只能用于声明掉线方")
        if owner_id not in self.active:
            self._reject("只有活跃拥有方可以释放恢复材料")
        share = self.shares.get((owner_id, holder_id))
        if share is None:
            self._reject("缺少必要份额")
        key = (owner_id, holder_id)
        if key in self.recovery_material:
            self._reject(f"重复重传恢复材料 {key}，拒绝")
        self.recovery_material[key] = share.value
        return share

    def finalize_recovery(self):
        self._require_phase(Phase.RECOVERY)
        self.phase = Phase.AGGREGATE

    # ---- 阶段五：聚合 ----

    def submit_partial(self, holder_id):
        """活跃持有方提交部分和：其持有的、属于活跃拥有者的份额之和（模 p）。"""
        self._require_phase(Phase.AGGREGATE)
        if holder_id not in self.active:
            self._reject("掉线方不能参与聚合")
        if holder_id in self.partials:
            self._reject(f"重复重传部分和 {holder_id!r}，拒绝")
        active_owners = set(self.active)
        party = self.parties[holder_id]
        partial = sum(
            s.value
            for s in party.inbox
            if s.owner in active_owners and s.round_id == self.round_id
        ) % self.prime
        self.partials[holder_id] = partial
        return partial

    def aggregate(self):
        """汇总。阈值不足或缺少必要份额/恢复材料时返回 INCOMPLETE。"""
        self._require_phase(Phase.AGGREGATE)
        active = self.active
        if len(active) < self.threshold:
            self.result = RoundResult(
                self.round_id,
                RoundStatus.INCOMPLETE,
                None,
                None,
                f"活跃方 {len(active)} 低于阈值 {self.threshold}，"
                "拒绝输出任何部分和",
            )
        else:
            missing_recovery = [
                (o, d)
                for o in active
                for d in sorted(self.dropped)
                if (o, d) not in self.recovery_material
            ]
            missing_partials = [h for h in active if h not in self.partials]
            if missing_recovery or missing_partials:
                self.result = RoundResult(
                    self.round_id,
                    RoundStatus.INCOMPLETE,
                    None,
                    None,
                    "缺少必要份额/恢复材料: "
                    f"恢复材料缺失 {missing_recovery}，"
                    f"部分和缺失 {missing_partials}",
                )
            else:
                recovered = sum(self.recovery_material.values()) % self.prime
                total = (sum(self.partials.values()) + recovered) % self.prime
                self.result = RoundResult(
                    self.round_id, RoundStatus.COMPLETE, total, active, None
                )
        self.phase = Phase.CLOSED
        return self.result


def run_round(round_id, inputs, threshold, prime=DEFAULT_PRIME, dropped=(), rng=None):
    """驱动一轮完整流程（合成数据）。返回 (RoundResult, Round)。"""
    rnd = Round(round_id, threshold, prime)
    for pid, value in inputs.items():
        rnd.commit(Party(pid, value))
    rnd.close_commitment()
    for pid in rnd.committed:
        for share in rnd.create_shares(pid, rng=rng):
            rnd.deliver_share(pid, share)
    rnd.close_sharing()
    rnd.confirm_dropouts(dropped)
    for owner_id in rnd.active:
        for d in sorted(rnd.dropped):
            rnd.release_recovery_material(owner_id, d)
    rnd.finalize_recovery()
    for holder_id in rnd.active:
        rnd.submit_partial(holder_id)
    return rnd.aggregate(), rnd
