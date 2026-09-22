"""Runnable samples for the bounded edit-distance state machine."""

from edit_machine import EditDistanceNFA

ALPHABET = "abcdefghijklmnopqrstuvwxyz"


def show(pattern, text, k, title, **kwargs):
    print(f"== {title}")
    print(f"pattern={pattern!r} text={text!r} k={k} kwargs={kwargs}")
    machine = EditDistanceNFA.build(pattern, ALPHABET, k, **kwargs)
    stream = machine.stream()
    print("states:", machine.states)
    for ch in text:
        stream.feed(ch)
        print(f" after {stream.steps} symbol(s): {stream.state_costs}")
    print(stream.result())
    print()


show("kitten", "sitting", 2, "classic Levenshtein: substitute + insert")
show("cat", "ct", 2, "deletion on the stream, explicit costs", deletion_cost=2)
show("", "abc", 3, "empty pattern accepts strings of length <= k")
show("ab", "ba", 1, "adjacent swap is NOT one edit by default")
show("ab", "ba", 1, "adjacent swap allowed when explicitly declared", transposition_cost=1)
