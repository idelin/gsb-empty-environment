"""Runnable samples for the edit-distance automaton. Run: python3.11 demo.py"""

from edit_distance_automaton import EditCosts, EditDistanceAutomaton


def show(title):
    print(f"\n=== {title} ===")


# 1. Classic example: minimum distance + edit path.
show("kitten -> sitting, unit costs, k=3")
aut = EditDistanceAutomaton("kitten", "aeikgnst", k=3)
accepted, dist, ops = aut.explain("sitting")
print("accepted:", accepted, "| min distance:", dist)
print("edit path:", " ".join(str(op) for op in ops))

# 2. Streaming: the state set after every consumed symbol.
show("streaming 'gambol' against pattern 'gumbo', k=2")
aut = EditDistanceAutomaton("gumbo", "abeglmou", k=2)
aut.reset()
print(f"  start: {aut.states}")
for ch in "gambol":
    aut.step(ch)
    print(f"  after {ch!r}: {aut.states}")
print("accepted:", aut.accepts(), "| best cost:", aut.best_cost())

# 3. Explicit weighted costs: substitution too expensive -> delete+insert.
show("weighted costs insert=1 delete=1 substitute=5, 'ab' -> 'cd'")
aut = EditDistanceAutomaton("ab", "abcd", k=10,
                            costs=EditCosts(insert=1, delete=1, substitute=5))
accepted, dist, ops = aut.explain("cd")
print("min distance:", dist)
print("edit path:", " ".join(str(op) for op in ops))

# 4. Adjacent swap: not a single edit unless declared.
show("transposition 'ab' -> 'ba'")
plain = EditDistanceAutomaton("ab", "ab", k=2)
print("default costs      :", plain.min_distance("ba"), "(del+ins or 2 subs)")
declared = EditDistanceAutomaton("ab", "ab", k=1, costs=EditCosts(transpose=1))
accepted, dist, ops = declared.explain("ba")
print("transpose declared :", dist, "|", " ".join(str(op) for op in ops))

# 5. Empty pattern matches exactly the strings of length <= k.
show("empty pattern, k=2")
aut = EditDistanceAutomaton("", "abc", k=2)
for text in ("", "a", "ab", "abc"):
    print(f"  {text!r:6} accepted={aut.match(text)[0]!s:5} "
          f"best_cost={aut.match(text)[1]}")
