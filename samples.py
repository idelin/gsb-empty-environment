"""三个演示模型: 无限重试(可能完成)、有界重试(公平必完成)、依赖阻塞。"""

from model import ProtocolModel, Transition

SYSTEM_GROUPS = ("validator", "scheduler", "worker", "retry", "network")


def build_retry_protocol() -> ProtocolModel:
    """带无限重试与 ACK 超时回环的协议: 存在成功路径,但不保证完成。"""
    return ProtocolModel(
        name="retry-protocol",
        states={
            "ACCEPTED", "VALIDATING", "QUEUED", "DISPATCHED", "PROCESSING",
            "RETRY_WAIT", "AWAITING_ACK",
            "COMPLETED", "CANCELLED", "REJECTED",
        },
        terminals={"COMPLETED", "CANCELLED", "REJECTED"},
        initial="ACCEPTED",
        fair_groups=SYSTEM_GROUPS,
        transitions=[
            Transition("ACCEPTED", "validate", "VALIDATING", "validator"),
            Transition("ACCEPTED", "cancel", "CANCELLED", "client"),
            Transition("VALIDATING", "validation_ok", "QUEUED", "validator"),
            Transition("VALIDATING", "validation_fail", "REJECTED", "validator"),
            Transition("VALIDATING", "cancel", "CANCELLED", "client"),
            Transition("QUEUED", "dispatch", "DISPATCHED", "scheduler"),
            Transition("QUEUED", "cancel", "CANCELLED", "client"),
            Transition("DISPATCHED", "start", "PROCESSING", "worker"),
            Transition("PROCESSING", "work_done", "AWAITING_ACK", "worker"),
            Transition("PROCESSING", "transient_error", "RETRY_WAIT", "worker"),
            Transition("RETRY_WAIT", "retry", "PROCESSING", "retry"),
            Transition("RETRY_WAIT", "cancel", "CANCELLED", "client"),
            Transition("AWAITING_ACK", "ack", "COMPLETED", "network"),
            Transition("AWAITING_ACK", "timeout", "RETRY_WAIT", "network"),
        ],
    )


def build_bounded_protocol() -> ProtocolModel:
    """重试次数有界且无回环的协议: 所有公平路径都到达终态。"""
    return ProtocolModel(
        name="bounded-protocol",
        states={
            "ACCEPTED", "QUEUED",
            "PROCESSING_1", "RETRY_1", "PROCESSING_2", "RETRY_2", "PROCESSING_3",
            "AWAITING_ACK",
            "COMPLETED", "CANCELLED", "FAILED",
        },
        terminals={"COMPLETED", "CANCELLED", "FAILED"},
        initial="ACCEPTED",
        fair_groups=("scheduler", "worker", "retry", "network"),
        transitions=[
            Transition("ACCEPTED", "enqueue", "QUEUED", "scheduler"),
            Transition("ACCEPTED", "cancel", "CANCELLED", "client"),
            Transition("QUEUED", "start", "PROCESSING_1", "worker"),
            Transition("QUEUED", "cancel", "CANCELLED", "client"),
            Transition("PROCESSING_1", "work_done", "AWAITING_ACK", "worker"),
            Transition("PROCESSING_1", "transient_error", "RETRY_1", "worker"),
            Transition("RETRY_1", "retry", "PROCESSING_2", "retry"),
            Transition("RETRY_1", "cancel", "CANCELLED", "client"),
            Transition("PROCESSING_2", "work_done", "AWAITING_ACK", "worker"),
            Transition("PROCESSING_2", "transient_error", "RETRY_2", "worker"),
            Transition("RETRY_2", "retry", "PROCESSING_3", "retry"),
            Transition("PROCESSING_3", "work_done", "AWAITING_ACK", "worker"),
            Transition("PROCESSING_3", "transient_error", "FAILED", "worker"),
            Transition("AWAITING_ACK", "ack", "COMPLETED", "network"),
        ],
    )


def build_blocked_protocol() -> ProtocolModel:
    """依赖永不就绪: 从 ACCEPTED 出发无法到达任何终态。"""
    return ProtocolModel(
        name="blocked-protocol",
        states={"ACCEPTED", "AWAITING_DEP", "COMPLETED"},
        terminals={"COMPLETED"},
        initial="ACCEPTED",
        fair_groups=("dep",),
        transitions=[
            Transition("ACCEPTED", "submit", "AWAITING_DEP", "client"),
            Transition("AWAITING_DEP", "wait", "AWAITING_DEP", "dep"),
        ],
    )
