"""有限域加法分享的多方求和协议模拟。

本包仅模拟协议流程（参与承诺、分享、掉线确认、恢复、聚合），
用于教学与状态探索，不构成任何密码学安全背书，也不是生产级密码协议实现。
"""

from .sharing import DEFAULT_PRIME, Share, reconstruct, split_secret
from .protocol import (
    Party,
    Phase,
    RejectionError,
    Round,
    RoundResult,
    RoundStatus,
    run_round,
)
from .verification import check_conservation, explore_states

__all__ = [
    "DEFAULT_PRIME",
    "Share",
    "split_secret",
    "reconstruct",
    "Party",
    "Phase",
    "RejectionError",
    "Round",
    "RoundResult",
    "RoundStatus",
    "run_round",
    "check_conservation",
    "explore_states",
]
