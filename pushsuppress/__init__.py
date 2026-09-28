"""Multi-channel push suppression decision package."""

from .model import UNSENT, Notification, Instance, parse_instance
from .checkers import (
    Violation,
    check_identity,
    check_time_window,
    check_user_rolling_limit,
    check_channel_capacity,
    check_mutex,
    verify_schedule,
)
from .solver import solve, enumerate_schedules, objective_value, tie_key

__all__ = [
    "UNSENT",
    "Notification",
    "Instance",
    "parse_instance",
    "Violation",
    "check_identity",
    "check_time_window",
    "check_user_rolling_limit",
    "check_channel_capacity",
    "check_mutex",
    "verify_schedule",
    "solve",
    "enumerate_schedules",
    "objective_value",
    "tie_key",
]
