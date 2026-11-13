# BFS counterexample traces

Scenario: high-integrity victim holds a read-only File handle (slot 0);
the low-integrity attacker holds nothing. Violation: attacker obtains an
effective Write it was never granted (or a duplicate bound to stale
generations succeeds). Search is breadth-first, so traces are minimal
within depth 5.

## generic mapping escalation

ReviewHost (pending review): VIOLATION in 1 step(s):

  1. dup desired=GenericWrite

Host (reviewed): no violation reachable within depth 5.

## sameAccess conflict

ReviewHost (pending review): VIOLATION in 1 step(s):

  1. dup sameAccess+GenericWrite

Host (reviewed): no violation reachable within depth 5.

## forged broker capability

ReviewHost (pending review): VIOLATION in 1 step(s):

  1. dup desired=Write forged-broker

Host (reviewed): no violation reachable within depth 5.

## close/reuse race

ReviewHost (pending review): VIOLATION in 3 step(s):

  1. close victim slot0
  2. victim reopens obj2 (read|write)
  3. dup stale-handle sameAccess

Host (reviewed): no violation reachable within depth 5.

## target process restart (stale binding)

ReviewHost (pending review): VIOLATION in 4 step(s):

  1. record refs
  2. issue broker cap
  3. restart attacker
  4. dup recorded-refs stored-cap

Host (reviewed): no violation reachable within depth 5.

## full action set

ReviewHost (pending review): VIOLATION in 1 step(s):

  1. dup desired=Write

Host (reviewed): no violation reachable within depth 5.

