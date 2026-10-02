"""有限域加法分享多方求和 —— 协议流程模拟器。

重要声明（务必阅读）：
- 本模块是【协议流程模拟器】，仅用于教学与状态机验证，只处理合成数据。
- 本模块【不构成】任何密码学安全背书：不防范串谋、不提供消息认证 /
  完整性保护、不防御恶意聚合者，也不声称实现了任何生产级密码协议。
- 任何基于本模拟器的统计实验都【不是】形式化隐私证明。
- 请勿将本模块用于真实敏感数据或生产环境。

模型定义：
- 有限域 F_p（p 为素数），所有份额与还原运算均在 mod p 下进行。
- 每轮有唯一 round_id；不同轮次的份额不能混用。
- 参与方集合在 COMMIT 阶段一次性承诺，之后不可变更。
- 每个参与方 i 持有输入 x_i ∈ F_p，将其拆成 n-of-n 加法份额
  s_{i->j}（j 遍历全部已承诺参与方），满足 sum_j s_{i->j} ≡ x_i (mod p)。
- 轮次输出定义为【幸存参与方】输入之和（mod p）。掉线方输入不计入，
  结果对象会明确列出有效参与集合与掉线集合，绝不以部分和冒充总和。
- 若幸存参与方数量低于阈值 t，轮次中止（ABORTED），不输出任何数值。
- 掉线恢复：幸存参与方 i 针对已声明掉线的 j 重新提交其原始份额
  s_{i->j} 作为恢复材料；恢复材料只能用于已声明掉线的参与方，
  且必须与原始分享一致。缺少必要恢复份额时返回 INCOMPLETE。
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from enum import Enum, auto

DEFAULT_PRIME = 2**61 - 1  # 梅森素数，模拟用默认有限域


# ---------------------------------------------------------------- 异常类型

class ProtocolError(Exception):
    """协议违规基类。"""


class WrongRoundError(ProtocolError):
    """份额属于其他轮次（旧轮分享 / 跨轮混用）。"""


class DuplicateShareError(ProtocolError):
    """重复提交 / 重传同一份额。"""


class UnknownPartyError(ProtocolError):
    """参与方不在本轮已承诺集合中。"""


class PhaseError(ProtocolError):
    """当前阶段不允许该操作。"""


class UnauthorizedRecoveryError(ProtocolError):
    """恢复材料未指向已声明掉线的参与方，或提交者不具备恢复资格。"""


class IncompleteSharesError(ProtocolError):
    """缺少必要份额，无法继续。"""


# ---------------------------------------------------------------- 数据结构

class Phase(Enum):
    COMMIT = auto()           # 参与集合承诺
    SHARE = auto()            # 分享
    CONFIRM_DROPOUT = auto()  # 掉线确认
    RECOVER = auto()          # 恢复
    CLOSED = auto()           # 已产出结果（COMPLETE / INCOMPLETE）
    ABORTED = auto()          # 阈值不足，已中止


class RoundStatus(Enum):
    COMPLETE = auto()    # 完成，total 为幸存参与方输入之和 (mod p)
    INCOMPLETE = auto()  # 缺少必要份额，未完成，无输出
    ABORTED = auto()     # 阈值不足，已中止，无输出


@dataclass(frozen=True)
class Share:
    """一份加法份额。round_id 用于轮次隔离。"""

    round_id: int
    dealer: str      # 分享者（输入所有者）
    recipient: str   # 份额接收者
    value: int       # 份额值，须在 [0, p)


@dataclass(frozen=True)
class RoundResult:
    """轮次结果。仅在 COMPLETE 时 total 非 None。"""

    status: RoundStatus
    round_id: int
    total: int | None
    effective_parties: tuple[str, ...]   # 计入总和的幸存参与方
    dropped: tuple[str, ...]             # 已确认掉线的参与方
    missing_recovery: tuple[tuple[str, str], ...] = ()  # 缺失的 (dealer, dropped) 恢复份额
    detail: str = ""


# ---------------------------------------------------------------- 基础运算

def is_prime(n: int) -> bool:
    """确定性 Miller-Rabin（对 n < 2^64 精确），用于校验有限域模数。"""
    if n < 2:
        return False
    for small in (2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37):
        if n == small:
            return True
        if n % small == 0:
            return False
    d = n - 1
    r = 0
    while d % 2 == 0:
        d //= 2
        r += 1
    for a in (2, 325, 9375, 28178, 450775, 9780504, 1795265022):
        if a % n == 0:
            continue
        x = pow(a, d, n)
        if x in (1, n - 1):
            continue
        for _ in range(r - 1):
            x = x * x % n
            if x == n - 1:
                break
        else:
            return False
    return True


def share_secret(
    secret: int,
    round_id: int,
    dealer: str,
    recipients: tuple[str, ...],
    prime: int,
    rng: random.Random,
) -> list[Share]:
    """把 secret 拆成 n-of-n 加法份额：sum(share) ≡ secret (mod p)。"""
    if not 0 <= secret < prime:
        raise ValueError(f"秘密值须位于 [0, p)：{secret}")
    if not recipients:
        raise ValueError("接收者集合不能为空")
    values = [rng.randrange(prime) for _ in range(len(recipients) - 1)]
    values.append((secret - sum(values)) % prime)
    return [Share(round_id, dealer, recipient, value)
            for recipient, value in zip(recipients, values)]


def reconstruct(shares: list[Share], prime: int) -> int:
    """还原：sum(share) mod p。不同轮次的份额混用会被拒绝。"""
    if not shares:
        raise IncompleteSharesError("没有任何份额，无法还原")
    round_ids = {share.round_id for share in shares}
    if len(round_ids) != 1:
        raise WrongRoundError(f"不同轮次的份额不能混用：{sorted(round_ids)}")
    return sum(share.value for share in shares) % prime


# ---------------------------------------------------------------- 轮次状态机

class Round:
    """一轮多方求和模拟：承诺 → 分享 → 掉线确认 → 恢复 → 输出。"""

    def __init__(
        self,
        round_id: int,
        prime: int = DEFAULT_PRIME,
        threshold: int = 1,
        rng: random.Random | None = None,
    ):
        if not is_prime(prime):
            raise ValueError(f"模数必须为素数（有限域 F_p）：{prime}")
        if threshold < 1:
            raise ValueError("阈值至少为 1")
        self.round_id = round_id
        self.prime = prime
        self.threshold = threshold
        self.rng = rng if rng is not None else random.Random()
        self.phase = Phase.COMMIT
        self._committed: tuple[str, ...] | None = None
        self._shares: dict[tuple[str, str], Share] = {}
        self._dropped: set[str] = set()
        self._recovery: dict[tuple[str, str], Share] = {}
        self._result: RoundResult | None = None

    # ---- 只读视图 ----

    @property
    def committed_parties(self) -> tuple[str, ...] | None:
        return self._committed

    @property
    def dropped_parties(self) -> tuple[str, ...]:
        return tuple(p for p in (self._committed or ()) if p in self._dropped)

    @property
    def survivors(self) -> tuple[str, ...]:
        return tuple(p for p in (self._committed or ()) if p not in self._dropped)

    # ---- 阶段一：参与集合承诺 ----

    def commit(self, parties: list[str] | tuple[str, ...]) -> None:
        self._require_phase(Phase.COMMIT)
        parties = tuple(parties)
        if not parties:
            raise ValueError("参与集合不能为空")
        if len(set(parties)) != len(parties):
            raise ValueError("参与集合中存在重复参与方")
        if self.threshold > len(parties):
            raise ValueError("阈值不能超过参与方数量")
        self._committed = parties
        self.phase = Phase.SHARE

    # ---- 阶段二：分享 ----

    def submit_share(self, share: Share) -> None:
        self._require_phase(Phase.SHARE)
        self._check_round(share)
        if not 0 <= share.value < self.prime:
            raise ValueError(f"份额值须位于 [0, p)：{share.value}")
        if share.dealer not in self._committed or share.recipient not in self._committed:
            raise UnknownPartyError(
                f"dealer={share.dealer!r} recipient={share.recipient!r} 不在已承诺集合中")
        key = (share.dealer, share.recipient)
        if key in self._shares:
            raise DuplicateShareError(f"重复提交份额 dealer={share.dealer!r} recipient={share.recipient!r}")
        self._shares[key] = share

    def deal(self, dealer: str, secret: int) -> list[Share]:
        """dealer 将其输入拆成加法份额并分发给全部已承诺参与方。"""
        self._require_phase(Phase.SHARE)
        if self._committed is None or dealer not in self._committed:
            raise UnknownPartyError(f"{dealer!r} 不在已承诺集合中")
        shares = share_secret(secret, self.round_id, dealer, self._committed, self.prime, self.rng)
        for share in shares:
            self.submit_share(share)
        return shares

    def close_sharing(self) -> None:
        self._require_phase(Phase.SHARE)
        missing = [(i, j) for i in self._committed for j in self._committed
                   if (i, j) not in self._shares]
        if missing:
            raise IncompleteSharesError(f"分享不完整，缺少 {len(missing)} 份，例如 {missing[0]}")
        self.phase = Phase.CONFIRM_DROPOUT

    # ---- 阶段三：掉线确认 ----

    def declare_dropout(self, party: str) -> None:
        self._require_phase(Phase.CONFIRM_DROPOUT)
        if party not in self._committed:
            raise UnknownPartyError(f"{party!r} 不在已承诺集合中")
        if party in self._dropped:
            raise ProtocolError(f"重复掉线声明：{party!r}")
        self._dropped.add(party)

    def close_dropout_confirmation(self) -> None:
        self._require_phase(Phase.CONFIRM_DROPOUT)
        if len(self.survivors) < self.threshold:
            self.phase = Phase.ABORTED
            self._result = RoundResult(
                status=RoundStatus.ABORTED,
                round_id=self.round_id,
                total=None,
                effective_parties=(),
                dropped=self.dropped_parties,
                detail=(f"幸存参与方 {len(self.survivors)} 低于阈值 {self.threshold}，"
                        "轮次中止，不输出任何部分和"),
            )
        else:
            self.phase = Phase.RECOVER

    # ---- 阶段四：恢复 ----

    def submit_recovery(self, share: Share) -> None:
        """幸存参与方针对已声明掉线方重新提交其原始份额 s_{dealer->recipient}。"""
        self._require_phase(Phase.RECOVER)
        self._check_round(share)
        if not 0 <= share.value < self.prime:
            raise ValueError(f"份额值须位于 [0, p)：{share.value}")
        if share.recipient not in self._dropped:
            raise UnauthorizedRecoveryError(
                f"恢复材料只能用于已声明掉线的参与方，{share.recipient!r} 未掉线")
        if share.dealer not in self.survivors:
            raise UnauthorizedRecoveryError(
                f"只有幸存参与方可提交恢复材料，{share.dealer!r} 不具备资格")
        original = self._shares.get((share.dealer, share.recipient))
        if original is None or original.value != share.value:
            raise ProtocolError("恢复份额与原始分享不一致，拒绝接受")
        key = (share.dealer, share.recipient)
        if key in self._recovery:
            raise DuplicateShareError(
                f"重复恢复份额 dealer={share.dealer!r} recipient={share.recipient!r}")
        self._recovery[key] = share

    # ---- 输出 ----

    def finalize(self) -> RoundResult:
        """汇总本轮结果。阈值不足 / 缺少必要份额时绝不输出数值。"""
        if self._result is not None:
            return self._result
        self._require_phase(Phase.RECOVER)
        survivors = self.survivors
        dropped = self.dropped_parties
        missing = tuple((i, j) for j in dropped for i in survivors
                        if (i, j) not in self._recovery)
        if missing:
            self._result = RoundResult(
                status=RoundStatus.INCOMPLETE,
                round_id=self.round_id,
                total=None,
                effective_parties=(),
                dropped=dropped,
                missing_recovery=missing,
                detail=f"缺少 {len(missing)} 份必要恢复份额，本轮未完成",
            )
        else:
            dropped_set = set(dropped)
            total = 0
            for i in survivors:
                for j in self._committed:
                    share = self._recovery[(i, j)] if j in dropped_set else self._shares[(i, j)]
                    total = (total + share.value) % self.prime
            self._result = RoundResult(
                status=RoundStatus.COMPLETE,
                round_id=self.round_id,
                total=total,
                effective_parties=survivors,
                dropped=dropped,
                detail="总和仅覆盖幸存参与方的输入（mod p）",
            )
        self.phase = Phase.CLOSED
        return self._result

    # ---- 内部工具 ----

    def _require_phase(self, phase: Phase) -> None:
        if self.phase is not phase:
            raise PhaseError(f"当前阶段 {self.phase.name}，不允许该操作（需要 {phase.name}）")

    def _check_round(self, share: Share) -> None:
        if share.round_id != self.round_id:
            raise WrongRoundError(
                f"份额属于轮次 {share.round_id}，当前轮次 {self.round_id}，拒绝混入")
