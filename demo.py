"""Runnable samples for the DPLL solver."""

from dpll import DPLLSolver, verify_model


def show(title, clauses, model, num_vars):
    print(f"--- {title}")
    print("clauses:", clauses)
    if model is None:
        print("result : UNSAT (search exhausted, no assignment exists)")
    else:
        assignment = {v: model[v] for v in range(1, num_vars + 1)}
        print("result : SAT")
        print("model  :", assignment)
        print("checker:", verify_model(clauses, model))
    print()


def pigeonhole(pigeons, holes):
    def var(p, h):
        return p * holes + h + 1

    clauses = []
    for p in range(pigeons):
        clauses.append([var(p, h) for h in range(holes)])
    for h in range(holes):
        for p1 in range(pigeons):
            for p2 in range(p1 + 1, pigeons):
                clauses.append([-var(p1, h), -var(p2, h)])
    return clauses, pigeons * holes


def main():
    # 1. Ordinary SAT instance; answer is verified by the independent checker.
    clauses = [[1, 2], [-1, 3], [-2, -3], [1, -4], [4, 2]]
    solver = DPLLSolver(num_vars=4)
    for clause in clauses:
        solver.add_clause(clause)
    show("sample 1: satisfiable formula", solver.original_clauses, solver.solve(), 4)

    # 2. Assumption solving + retraction on the same persistent solver.
    print("--- sample 2: solve under hypotheses (auto-retracted)")
    formula = [[-1, 2], [-2, 3]]
    s2 = DPLLSolver(num_vars=3)
    for clause in formula:
        s2.add_clause(clause)
    m1 = s2.solve(assumptions=[1])
    print("assume x1=true  ->", {v: m1[v] for v in (1, 2, 3)})
    m2 = s2.solve(assumptions=[-3])
    print("assume x3=false ->", {v: m2[v] for v in (1, 2, 3)})
    m3 = s2.solve(assumptions=[1, -3])
    print("assume x1 & ~x3 ->", "UNSAT" if m3 is None else m3)
    m4 = s2.solve()
    print("no assumptions  ->", {v: m4[v] for v in (1, 2, 3)}, "(hypotheses retracted)")
    print()

    # 3. UNSAT instance: 4 pigeons, 3 holes (pigeonhole principle).
    php, n = pigeonhole(4, 3)
    s3 = DPLLSolver(num_vars=n)
    for clause in php:
        s3.add_clause(clause)
    show("sample 3: pigeonhole 4 pigeons / 3 holes", php, s3.solve(), n)

    # 4. Edge cases.
    print("--- sample 4: edge cases")
    s4 = DPLLSolver(num_vars=2)
    print("empty formula ->", "SAT" if s4.solve() is not None else "UNSAT")
    s4.add_clause([1, -1])
    print("only tautology clause ->", "SAT" if s4.solve() is not None else "UNSAT")
    s4.add_clause([])
    print("after adding empty clause ->", "UNSAT" if s4.solve() is None else "SAT")


if __name__ == "__main__":
    main()
