"""Demo finite-state model: a wallet with overdraft and injectable faults.

State: (balance, limit, overdraft).  Config keys fall back to
``DEFAULT_CONFIG`` when absent, so "shrinking the initial configuration"
means deleting entries.

Faults (global flags, part of the input triple):

* ``lost_deposit``    -- every deposit is silently dropped.
* ``double_withdraw`` -- every withdraw is applied twice without rechecking
  its precondition (the injected bug).

Property violations (error signatures):

* ``NEGATIVE_BALANCE``        -- balance < 0 while overdraft is disabled.
* ``OVERDRAFT_BEYOND_LIMIT``  -- balance < -limit.
"""

from dataclasses import dataclass, replace

DEFAULT_CONFIG = {"balance": 0, "limit": 0, "overdraft": False}


@dataclass(frozen=True)
class WalletState:
    balance: int
    limit: int
    overdraft: bool


class WalletModel:
    def initial_state(self, config):
        merged = {**DEFAULT_CONFIG, **config}
        return WalletState(
            balance=merged["balance"],
            limit=merged["limit"],
            overdraft=merged["overdraft"],
        )

    def precondition(self, state, action):
        name = action[0]
        if name == "deposit":
            return action[1] >= 1
        if name == "withdraw":
            amount = action[1]
            available = state.balance + (state.limit if state.overdraft else 0)
            return 1 <= amount <= available
        if name == "set_limit":
            return action[1] >= 0
        if name in ("enable_overdraft", "disable_overdraft"):
            return True
        return False

    def step(self, state, action, faults):
        name = action[0]
        if name == "deposit":
            if "lost_deposit" in faults:
                return state
            return replace(state, balance=state.balance + action[1])
        if name == "withdraw":
            amount = action[1]
            balance = state.balance - amount
            if "double_withdraw" in faults:
                balance -= amount  # bug: applied twice, no precondition recheck
            return replace(state, balance=balance)
        if name == "set_limit":
            return replace(state, limit=action[1])
        if name == "enable_overdraft":
            return replace(state, overdraft=True)
        if name == "disable_overdraft":
            return replace(state, overdraft=False)
        raise ValueError(f"unknown action: {action!r}")

    def check(self, state, action, faults):
        if state.balance < 0 and not state.overdraft:
            return "NEGATIVE_BALANCE"
        if state.balance < -state.limit:
            return "OVERDRAFT_BEYOND_LIMIT"
        return None


def initial_counterexample():
    """A noisy counterexample: redundant config, trace actions and faults."""
    config = {"balance": 10, "limit": 5, "overdraft": True, "log_level": "debug"}
    trace = [
        ("deposit", 4),
        ("set_limit", 8),
        ("withdraw", 3),
        ("deposit", 2),
        ("withdraw", 7),
        ("disable_overdraft",),
        ("enable_overdraft",),
        ("withdraw", 9),
    ]
    faults = frozenset({"lost_deposit", "double_withdraw"})
    return config, trace, faults
