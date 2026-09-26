"""Runnable demo for regular_filter: DFA-constrained sequence filtering."""

from regular_filter import DFA, RegularFilter, check_solution


def even_ones_dfa():
    """Accepts binary words containing an even number of 1s."""
    transitions = {
        (0, 0): 0, (0, 1): 1,   # state 0: even so far
        (1, 0): 1, (1, 1): 0,   # state 1: odd so far
    }
    return DFA(num_states=2, start=0, accepts={0}, transitions=transitions)


def dead_path_dfa():
    """Value 2 from the start only reaches a dead sink (state 2)."""
    transitions = {
        (0, 0): 1, (0, 1): 1, (0, 2): 2,
        (1, 0): 1, (1, 1): 1, (1, 2): 2,
        (2, 0): 2, (2, 1): 2, (2, 2): 2,
    }
    return DFA(num_states=3, start=0, accepts={1}, transitions=transitions)


def show(title, filt):
    print(f"  {title}")
    print(f"    domains after filtering: {[sorted(d) for d in filt.domains]}")
    print(f"    satisfiable: {filt.is_satisfiable()}")


def main():
    print("=== 1. Basic filtering: even number of 1s, length 4 ===")
    dfa = even_ones_dfa()
    filt = RegularFilter(dfa, [{0, 1}] * 4)
    show("all domains {0,1}:", filt)
    word = filt.solve()
    ok = check_solution(dfa, filt.original_domains, word)
    print(f"    solve() -> {word}, independent checker: {ok}")

    print("\n=== 2. Dead-path values are removed ===")
    filt2 = RegularFilter(dead_path_dfa(), [{0, 1, 2}, {0, 1, 2}])
    show("value 2 only reaches a dead sink:", filt2)

    print("\n=== 3. Incremental updates ===")
    filt3 = RegularFilter(even_ones_dfa(), [{0, 1}] * 3)
    show("initial (length 3):", filt3)
    filt3.assign(0, 1)
    show("after assign(0, 1):", filt3)
    filt3.assign(1, 1)
    show("after assign(1, 1)  -> position 2 forced to 0:", filt3)
    word3 = filt3.solve()
    ok3 = check_solution(even_ones_dfa(), filt3.original_domains, word3)
    print(f"    solve() -> {word3}, checker: {ok3}")
    filt3.remove_value(2, 0)
    show("after remove_value(2, 0) -> unsatisfiable:", filt3)

    print("\n=== 4. Empty sequence: start-state acceptance decides ===")
    empty_ok = RegularFilter(even_ones_dfa(), [])
    print(f"  start accepting   -> satisfiable: {empty_ok.is_satisfiable()}, "
          f"solve() -> {empty_ok.solve()}")
    dfa_no = DFA(2, 0, {1}, {(0, 0): 0, (0, 1): 1, (1, 0): 1, (1, 1): 1})
    empty_no = RegularFilter(dfa_no, [])
    print(f"  start not accepting -> satisfiable: {empty_no.is_satisfiable()}, "
          f"solve() -> {empty_no.solve()}")


if __name__ == "__main__":
    main()
