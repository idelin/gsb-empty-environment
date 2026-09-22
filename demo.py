"""End-to-end demonstration run against the local JSON fixtures."""
from pathlib import Path

from vsets import (
    BuildInput,
    PackageIndex,
    brute_solve,
    explain,
    parse_spec,
    solve,
    trace_lock_change,
    Version,
)
from vsets.solver import Unsatisfiable

FIXTURES = Path(__file__).parent / "tests" / "fixtures"


def main() -> None:
    index = PackageIndex.load(FIXTURES / "packages.json")

    print("== interval difference ==")
    left = parse_spec("[1.0.0,3.0.0)")
    right = parse_spec("[2.0.0,4.0.0)")
    print(f"[1,3) - [2,4) = {(left - right).serialize()}")
    print(f"smallest counterexample vs [2,4): {left.min_counterexample_against(right)}")

    print("\n== prerelease channels ==")
    alpha = parse_spec("[2.0.0,3.0.0)@alpha")
    print(f"2.0.0-alpha.1 in @alpha window: {Version.parse('2.0.0-alpha.1') in alpha}")
    print(f"2.0.0-beta.1  in @alpha window: {Version.parse('2.0.0-beta.1') in alpha}")
    print(f"2.0.0         in @alpha window: {Version.parse('2.0.0') in alpha}")

    print("\n== compatible cycle ==")
    build = BuildInput.load(FIXTURES / "build_basic.json")
    solution = solve(index, build.roots, build.context)
    brute = brute_solve(index, build.roots, build.context)
    print("smart:", solution.as_dict())
    print("brute:", brute.as_dict())

    print("\n== unsatisfiable cycle explanation ==")
    conflict_roots = list(build.roots)
    from vsets import Dependency
    from vsets.target import ConditionalVersionSet
    conflicting = [Dependency("c", ConditionalVersionSet.parse("[1.0.0,3.0.0)"))]
    try:
        solve(index, conflicting)
    except Unsatisfiable as error:
        print(explain(error).render())

    print("\n== lock change tracing ==")
    old = BuildInput.load(FIXTURES / "build_lock_old.json")
    new = BuildInput.load(FIXTURES / "build_lock_new.json")
    report = trace_lock_change(index, old.roots,
                               new.context, old.locked, new.locked)
    for cause in report.causes:
        print(f"{cause.package}: {cause.old_version} -> {cause.new_version} ({cause.reason})")


if __name__ == "__main__":
    main()
