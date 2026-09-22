"""Standalone executable counterexamples.

Run:  python3.11 counterexample.py

They disprove two tempting but wrong claims about the machine:

1. "An adjacent transposition is always one edit away."
   False under classic Levenshtein: 'ba' vs 'ab' costs 2 unless the caller
   explicitly declares a transposition cost.

2. "The empty pattern accepts every short-looking string within k edits."
   False: an empty pattern only matches strings of length <= k; length k+1
   needs k+1 insertions and is rejected.
"""

from edit_machine import EditDistanceNFA

failures = []


def check(name, condition, detail):
    status = "PASS" if condition else "FAIL"
    print(f"[{status}] {name}: {detail}")
    if not condition:
        failures.append(name)


alphabet = "abcd"

machine = EditDistanceNFA.build("ab", alphabet, k=1)
result = machine.stream().feed_all("ba").result()
check(
    "swap costs 2 by default",
    not result.accepted,
    f"'ba' vs 'ab' accepted={result.accepted} (distance 2 > k=1)",
)

machine = EditDistanceNFA.build("ab", alphabet, k=1, transposition_cost=1)
result = machine.stream().feed_all("ba").result()
check(
    "swap costs 1 only if declared",
    result.accepted and result.distance == 1,
    f"'ba' vs 'ab' accepted={result.accepted} distance={result.distance}",
)

machine = EditDistanceNFA.build("", alphabet, k=3)
accepted = machine.stream().feed_all("abc").result().accepted
rejected = machine.stream().feed_all("abcd").result().accepted
check(
    "empty pattern: len <= k",
    accepted and not rejected,
    f"'abc' accepted={accepted}, 'abcd' accepted={rejected}",
)

if failures:
    raise SystemExit(f"counterexamples failed: {failures}")
print("all counterexamples hold")
