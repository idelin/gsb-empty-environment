#!/usr/bin/env python3
"""Runnable tour of the regexlite engine.  Run:  python3 demo.py"""
from regexlite import Regex, build_nfa, parse
from regexlite.fuzz import check_pattern

LINE = "-" * 64


def show(title):
    print(f"\n{LINE}\n{title}\n{LINE}")


# 1. Parse tree with source positions -------------------------------------
show("1. Parsing: AST keeps source spans")
parsed = parse("(?<key>[a-z]*)=(?<val>[0-9]*)")
for label, idx in parsed.labels.items():
    print(f"  capture {label!r} -> group index {idx}")
print(f"  root span: {parsed.ast.span}  (pattern length {len(parsed.pattern)})")

# 2. NFA with explicit alphabet -------------------------------------------
show("2. Tagged epsilon-NFA over an explicit alphabet")
alpha = "abcdefghijklmnopqrstuvwxyz0123456789="
nfa = build_nfa(parsed, alpha)
print(f"  {nfa.nstates} states, alphabet of {len(nfa.alphabet)} symbols, "
      f"{nfa.nslots} tag slots")
print(f"  state 0 origin span in pattern: {nfa.origins[0]}")

# 3. Matching with labelled captures --------------------------------------
show("3. Leftmost-longest search with labelled captures")
rx = Regex.compile(parsed.pattern, alphabet=alpha)
m = rx.search("xx foo=123 y")
print(f"  search('xx foo=123 y') -> {m.span} matched={m.matched!r} "
      f"via {m.engine}")
print(f"  key={m.group('key')!r} at {m.captures['key']}, "
      f"val={m.group('val')!r} at {m.captures['val']}")

# 4. Deterministic priority rules -----------------------------------------
show("4. Priority: longest extent wins; ties resolved by branch order")
for pat, text in [("a|ab", "ab"), ("(a|ab)(c|bcd)(d*)", "abcd")]:
    r = Regex.compile(pat, alphabet="abcd")
    mm = r.search(text)
    print(f"  {pat!r} on {text!r} -> span={mm.span} captures={mm.captures}")

# 5. Empty string vs empty language ---------------------------------------
show("5. Empty string is not the empty language")
for pat in ["()", "[]", "([])*"]:
    r = Regex.compile(pat, alphabet="ab")
    print(f"  {pat!r:8} search('') -> {r.search('')}")

# 6. Nested stars terminate ------------------------------------------------
show("6. Nested stars terminate (empty iterations are forbidden)")
r = Regex.compile("(a*)*", alphabet="ab")
print(f"  '(a*)*' on 'aaaa' -> {r.search('aaaa').span}")

# 7. Streaming with longest-match waiting ---------------------------------
show("7. Chunked input: matches are held until they are final")
rx7 = Regex.compile("ab|abc", alphabet="abcx")
st = rx7.stream()
print(f"  feed('ab') -> {st.feed('ab')}   (waiting: 'abc' may still extend)")
print(f"  feed('c')  -> {st.feed('c')}")
print(f"  finish()   -> {st.finish()}")

# 8. Determinization budget ------------------------------------------------
show("8. State budget: incomplete is reported, never a wrong automaton")
tight = Regex.compile("(a|b)*abb", alphabet="ab", state_budget=2)
m8 = tight.search("aabbb")
print(f"  budget=2  -> {m8.matched!r} via {m8.engine}, "
      f"dfa_status={tight.dfa_status}")
roomy = Regex.compile("(a|b)*abb", alphabet="ab", state_budget=64)
m8b = roomy.search("aabbb")
print(f"  budget=64 -> {m8b.matched!r} via {m8b.engine}, "
      f"dfa_status={roomy.dfa_status}, "
      f"distinct DFA states={roomy.determinizer.distinct_states}")

# 9. Cache eviction is semantics-preserving --------------------------------
show("9. LRU eviction changes only recomputation, never results")
for cache in (1, 1000):
    r = Regex.compile("(a|b)*abb", alphabet="ab", cache_size=cache)
    hits = [r.search(t).span for t in ("abb", "aabb", "babb", "ababb")]
    print(f"  cache_size={cache:5} -> spans {hits}, "
          f"materializations={r.determinizer.materializations}")

# 10. Exhaustive cross-check against the reference backtracker -------------
show("10. Fuzz: pike VM + DFA vs independent backtracker (exhaustive)")
for pat in ["(a*)*", "(a|ab)*b", "(a*)(b*)"]:
    mm, skipped = check_pattern(pat, "ab", max_len=5)
    verdict = "MISMATCH " + str(mm) if mm else f"OK ({63 - skipped} strings)"
    print(f"  {pat!r:12} exhaustive over 'ab' <= 5: {verdict}")

print()
