"""有限域加法分享（仅用于协议流程模拟，非密码学实现）。

定义：秘密 s 在模 p 有限域上拆分为 n 个份额 v_0..v_{n-1}，
满足 sum(v_i) ≡ s (mod p)。还原必须集齐全部 n 个份额，
缺少任何必要份额都无法还原，本模块以抛出 ValueError 表示"未完成"。
"""

from __future__ import annotations

import secrets
from dataclasses import dataclass

DEFAULT_PRIME = 2**127 - 1  # 梅森素数，模拟用默认有限域模数


@dataclass(frozen=True)
class Share:
    """带轮次标签的加法份额。

    round_id 用于轮次隔离：不同轮次的份额不能混用。
    owner 为秘密拥有者，holder 为份额的目标持有方。
    """

    round_id: str
    owner: str
    holder: str
    index: int
    value: int


def split_secret(secret, n, prime, round_id, owner, holders, rng=None):
    """把 secret 拆成 n 个模 p 加法份额，每个持有者各得一份。"""
    if n < 2:
        raise ValueError("至少需要 2 个份额")
    if len(holders) != n:
        raise ValueError("持有者数量与份额数不一致")
    if len(set(holders)) != n:
        raise ValueError("持有者不可重复")
    secret %= prime
    rng = rng or secrets.SystemRandom()
    values = [rng.randrange(prime) for _ in range(n - 1)]
    values.append((secret - sum(values)) % prime)
    return [
        Share(round_id, owner, holder, i, value)
        for i, (holder, value) in enumerate(zip(holders, values))
    ]


def reconstruct(shares, prime, expected_round=None, expected_owner=None):
    """由份额还原秘密（模 p）。

    拒绝：空份额集、不同轮次混用、不同拥有者混用、重复份额。
    """
    shares = list(shares)
    if not shares:
        raise ValueError("缺少必要份额，无法还原")
    round_ids = {s.round_id for s in shares}
    if len(round_ids) != 1:
        raise ValueError("不同轮次份额不能混用")
    if expected_round is not None and round_ids != {expected_round}:
        raise ValueError(f"份额不属于轮次 {expected_round!r}")
    owners = {s.owner for s in shares}
    if len(owners) != 1:
        raise ValueError("不同拥有者的份额不能混用")
    if expected_owner is not None and owners != {expected_owner}:
        raise ValueError(f"份额不属于拥有者 {expected_owner!r}")
    indices = [s.index for s in shares]
    if len(set(indices)) != len(indices):
        raise ValueError("重复份额，拒绝还原")
    return sum(s.value for s in shares) % prime
