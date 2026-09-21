#!/usr/bin/env python3
"""Runnable demos for the license-expression dependency solver.

All rules are synthetic modeling rules from local JSON; no legal conclusions.

Run:  python3 main.py
"""

from pathlib import Path

from license_solver import (
    OPTIMAL,
    Solver,
    greedy_select,
    load_build,
    load_repository,
    repair,
)

SAMPLES = Path(__file__).resolve().parent / "samples"


def show_selection(result):
    if not result.feasible:
        print("    (no feasible selection)")
        return
    for name in sorted(result.selection):
        sel = result.selection[name]
        flags = []
        if sel.needs_exception:
            flags.append("EXCEPTION")
        if "disclose" in sel.obligations:
            flags.append("disclose")
        if sel.propagated_from:
            flags.append(f"propagated from {list(sel.propagated_from)}")
        notes = [note for lic in sel.licenses for note in lic.notes]
        print(
            f"    {name:<8} {str(sel.version):<8} {sel.license_text:<45} "
            f"{'; '.join(flags + notes)}"
        )
    exc, disc = result.cost
    print(f"    cost: exceptions={exc}, disclosures={disc} (shared deps counted once)")


def demo_trap():
    print("=" * 78)
    print("1. Three-package greedy trap: left / right / shared (static, proprietary)")
    print("=" * 78)
    repo = load_repository(SAMPLES / "repo.json")
    build = load_build(SAMPLES / "build_trap.json")
    greedy = greedy_select(repo, build)
    print("  Per-package greedy (highest version, cheapest local branch):")
    for name, (pv, _combo) in sorted(greedy.choices.items()):
        print(f"    picked {name}@{pv.version} ({pv.license_text})")
    print(f"    unresolved: {greedy.unresolved or '-'}")
    print(f"    conflicts: {greedy.conflicts or '-'}")
    print("  -> locally valid picks, globally stuck: left@2.0.0 needs shared>=2.0.0")
    print("     while right@1.0.0 needs shared<2.0.0 (and shared@2.0.0 is GPL-2.0-only,")
    print("     which the synthetic rule marks incompatible with Apache-2.0).")
    result = Solver(repo, build).solve()
    print(f"\n  Global solver: status={result.status} ({result.message})")
    print("  Feasible alternative branch: left@1.0.0 taking the MIT side of its OR,")
    print("  which admits shared@1.0.0 (MIT) next to right@1.0.0 (Apache-2.0):")
    show_selection(result)


def demo_diamond():
    print()
    print("=" * 78)
    print("2. Diamond graph: ui and net share jsonlib (per-edge dynamic link)")
    print("=" * 78)
    repo = load_repository(SAMPLES / "repo.json")
    build = load_build(SAMPLES / "build_diamond.json")
    result = Solver(repo, build).solve()
    print(f"  status={result.status}; jsonlib appears once and is counted once,")
    print("  weak copyleft does not propagate through the dynamic link edges:")
    show_selection(result)


def demo_with():
    print()
    print("=" * 78)
    print("3. WITH is conditional, never an unconditional allowance")
    print("=" * 78)
    repo = load_repository(SAMPLES / "repo.json")
    build = load_build(SAMPLES / "build_with.json")
    for held in (True, False):
        build.attributes["independent_modules"] = held
        result = Solver(repo, build).solve()
        sel = result.selection["guilib"]
        print(f"  independent_modules={held}: cost={result.cost}, "
              f"obligations={sorted(sel.obligations)}, tags={sorted(sel.effective_tags)}")
        for lic in sel.licenses:
            for note in lic.notes:
                print(f"    note: {note}")


def demo_budget():
    print()
    print("=" * 78)
    print("4. Search budget exhaustion: feasible upper bound / undecided")
    print("=" * 78)
    repo = load_repository(SAMPLES / "repo_app_v2.json")
    build = load_build(SAMPLES / "build_app.json")
    full = Solver(repo, build).solve()
    print(f"  full search: status={full.status}, nodes={full.nodes}, cost={full.cost}")
    for budget in (2, max(full.nodes // 2, 1), max(full.nodes - 1, 1)):
        partial = Solver(repo, build, budget=budget).solve()
        print(f"  budget={budget}: status={partial.status}, cost={partial.cost}")
        print(f"    {partial.message}")


def demo_repair():
    print()
    print("=" * 78)
    print("5. Incremental repair after a version change, with conflict core")
    print("=" * 78)
    build = load_build(SAMPLES / "build_app.json")
    base = Solver(load_repository(SAMPLES / "repo_app_v1.json"), build).solve()
    assert base.status == OPTIMAL
    previous = {n: (str(s.version), s.branches) for n, s in base.selection.items()}
    compact = {n: v[0] for n, v in sorted(previous.items())}
    print(f"  baseline (v1 repo): cost={base.cost}, selection={compact}")
    print("\n  5a. shared bumped to 1.1.0 (relicensed GPL-2.0-only), v2 repo has")
    print("      permissive netlib@1.1.0 / plugin@1.1.0 available:")
    result = repair(load_repository(SAMPLES / "repo_app_v2.json"), build,
                    previous, {"shared": "1.1.0"})
    print(f"    strategy={result.strategy}, status={result.status}, cost={result.cost}")
    print(f"    conflict core: {result.conflict_core}")
    print(f"    {result.message}")
    show_selection(result)
    print("\n  5b. same bump against the v3 repo (no permissive alternatives):")
    result = repair(load_repository(SAMPLES / "repo_app_v3.json"), build,
                    previous, {"shared": "1.1.0"})
    print(f"    strategy={result.strategy}, status={result.status}")
    print(f"    conflict core: {result.conflict_core}")
    print(f"    {result.message}")


def main():
    demo_trap()
    demo_diamond()
    demo_with()
    demo_budget()
    demo_repair()


if __name__ == "__main__":
    main()
