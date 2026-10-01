"""Finite-state model of a reusable barrier with dynamic membership.

Design notes
------------
* Participants may join/leave between rounds, cancel their wait, and
  disconnect/reconnect.  The barrier shrinks dynamically when a member
  leaves or disconnects mid-round.
* Arrivals and release notifications travel through explicit one-slot
  asynchronous channels (``arr_msg`` / ``rel_msg``), so stale messages
  from an older round are visible in the state space.
* The round change has a single explicit commit point (the ``commit``
  action): it releases every waiter, advances the round, clears the
  arrival set and folds in pending joins, atomically.
* Everything is a pure function of the state; the model is the *only*
  object under verification.  Bug flags below exist to demonstrate that
  the checker finds the hazards they introduce -- they are not part of
  the correct protocol.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import NamedTuple

OUT = "OUT"            # not joined
ACTIVE = "ACTIVE"      # joined, not waiting
WAITING = "WAITING"    # arrival accepted for the current round
RELEASED = "RELEASED"  # committed, release notification not yet delivered
DOWN = "DOWN"          # disconnected

NO_MSG = -1

COMMIT = ("commit", -1)


class State(NamedTuple):
    round: int                 # current barrier round (mod n_rounds)
    members: frozenset         # participants expected this round
    arrived: frozenset         # members whose arrival was accepted this round
    pending_join: frozenset    # joined mid-round; become members at next commit
    status: tuple              # per participant: OUT/ACTIVE/WAITING/RELEASED/DOWN
    wait_round: tuple          # round tag of the participant's accepted arrival
    arr_msg: tuple             # in-flight arrival message (round tag) or NO_MSG
    rel_msg: tuple             # in-flight release notification (round tag) or NO_MSG


@dataclass(frozen=True)
class BarrierModel:
    n: int = 3                 # number of participants
    n_rounds: int = 3          # round tags are taken modulo this bound
    # --- fault-injection flags (correct model: all False) ---
    bug_stale_arrival: bool = False    # proc_arrive ignores the round tag
    bug_stale_release: bool = False    # deliver releases a waiter regardless of tag
    bug_commit_no_reset: bool = False  # commit does not advance round / clear arrived
    bug_no_commit: bool = False        # commit action is never enabled
    blocking_wait: bool = False        # waiters cannot cancel/leave/disconnect

    # ------------------------------------------------------------------ #
    def initial_state(self) -> State:
        return State(
            round=0,
            members=frozenset(),
            arrived=frozenset(),
            pending_join=frozenset(),
            status=(OUT,) * self.n,
            wait_round=(NO_MSG,) * self.n,
            arr_msg=(NO_MSG,) * self.n,
            rel_msg=(NO_MSG,) * self.n,
        )

    # ------------------------------------------------------------------ #
    def actions(self, s: State):
        """Enabled actions, in a deterministic order."""
        acts = []
        for p in range(self.n):
            st = s.status[p]
            if st == OUT:
                acts.append(("join", p))
            elif st == ACTIVE:
                if s.arr_msg[p] == NO_MSG:
                    acts.append(("send_arrive", p))
                acts.append(("leave", p))
                acts.append(("disconnect", p))
            elif st == WAITING:
                if not self.blocking_wait:
                    acts.append(("cancel", p))
                    acts.append(("leave", p))
                    acts.append(("disconnect", p))
            elif st == RELEASED:
                if not self.blocking_wait:
                    acts.append(("cancel", p))
                    acts.append(("leave", p))
                    acts.append(("disconnect", p))
            elif st == DOWN:
                acts.append(("reconnect", p))
            if s.arr_msg[p] != NO_MSG:
                acts.append(("proc_arrive", p))
            if s.rel_msg[p] != NO_MSG:
                acts.append(("deliver", p))
        if (not self.bug_no_commit) and s.members and s.arrived == s.members:
            acts.append(COMMIT)
        return acts

    # ------------------------------------------------------------------ #
    def step(self, s: State, action) -> State:
        name, p = action
        st = list(s.status)
        wr = list(s.wait_round)
        arr = list(s.arr_msg)
        rel = list(s.rel_msg)
        members = s.members
        arrived = s.arrived
        pending = s.pending_join
        rnd = s.round

        if name == "join":
            st[p] = ACTIVE
            if not arrived:                    # barrier idle: join takes effect now
                members = members | {p}
            else:                              # mid-round: effective at next commit
                pending = pending | {p}

        elif name == "send_arrive":
            arr[p] = rnd                       # message carries the round the sender saw

        elif name == "proc_arrive":
            tag = arr[p]
            arr[p] = NO_MSG
            fresh = (tag == rnd) or self.bug_stale_arrival
            if p in members and st[p] == ACTIVE and fresh:
                st[p] = WAITING
                arrived = arrived | {p}
                wr[p] = tag
            # otherwise: stale or non-member arrival -> discarded

        elif name == "cancel":
            # WAITING: withdraw arrival (dynamic shrink).  RELEASED: give up
            # waiting for the notification; the in-flight message stays.
            if st[p] == WAITING:
                arrived = arrived - {p}
            st[p] = ACTIVE

        elif name in ("leave", "disconnect"):
            st[p] = OUT if name == "leave" else DOWN
            members = members - {p}
            arrived = arrived - {p}
            pending = pending - {p}
            wr[p] = NO_MSG
            arr[p] = NO_MSG
            rel[p] = NO_MSG
            # a departing participant's in-flight messages are dropped;
            # staleness hazards live on participants that stay joined.

        elif name == "reconnect":
            st[p] = OUT
            arr[p] = NO_MSG
            rel[p] = NO_MSG
            wr[p] = NO_MSG

        elif name == "deliver":
            tag = rel[p]
            rel[p] = NO_MSG
            if st[p] == RELEASED and tag == wr[p]:
                st[p] = ACTIVE                 # genuine release for this wait
            elif st[p] == WAITING and self.bug_stale_release:
                st[p] = ACTIVE                 # BUG: stale notification wakes a waiter
            # otherwise: stale notification -> discarded

        elif name == "commit":
            for q in arrived:
                st[q] = RELEASED
                rel[q] = rnd
            if not self.bug_commit_no_reset:
                rnd = (rnd + 1) % self.n_rounds
                arrived = frozenset()
            members = members | pending
            pending = frozenset()

        else:  # pragma: no cover - defensive
            raise ValueError(f"unknown action {action}")

        return State(rnd, frozenset(members), frozenset(arrived),
                     frozenset(pending), tuple(st), tuple(wr),
                     tuple(arr), tuple(rel))

    # ------------------------------------------------------------------ #
    # State invariants (checked on every reached state)
    # ------------------------------------------------------------------ #
    def invariants(self, s: State):
        v = []
        if not s.arrived <= s.members:
            v.append("I1:arrived-not-subset-of-members")
        for p in range(self.n):
            if p in s.arrived:
                if s.status[p] != WAITING:
                    v.append(f"I3:arrived-but-not-waiting:p{p}")
                if s.wait_round[p] != s.round:
                    v.append(f"I2:stale-arrival-counted:p{p}:"
                             f"wait_round={s.wait_round[p]}:round={s.round}")
            if s.status[p] == WAITING and p not in s.arrived:
                v.append(f"I3b:waiting-but-not-arrived:p{p}")
            if s.status[p] == RELEASED:
                if s.rel_msg[p] == NO_MSG or s.rel_msg[p] != s.wait_round[p]:
                    v.append(f"I4:released-without-matching-notification:p{p}")
                if s.wait_round[p] == s.round:
                    v.append(f"I5:released-in-current-round:p{p}")
        return v

    # ------------------------------------------------------------------ #
    # Transition assertions (checked on every fired transition)
    # ------------------------------------------------------------------ #
    def transition_assertions(self, s: State, action, s2: State):
        name, p = action
        v = []
        if name == "commit":
            if not s.members or s.arrived != s.members:
                v.append("T2:commit-without-full-house")
            if any(s.status[q] != WAITING for q in s.arrived):
                v.append("T1:double-release-same-round")
        if name == "deliver" and s.status[p] == WAITING and s2.status[p] == ACTIVE:
            v.append(f"T3:stale-notification-released-waiter:p{p}")
        return v
