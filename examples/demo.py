"""Solver behaviour demo; run from the repo root: python3.11 examples/demo.py"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from versionset import Repo, Resolver, Unsatisfiable, Version, VersionSet

repo = Repo.from_json_file("fixtures/repo_basic.json")
reqs = {"app": VersionSet.parse("[1.0.0,2.0.0)")}
resolver = Resolver(repo, "linux")

print("== 锁版本变更追踪 (lib: 1.5.0 -> 锁定 1.2.0) ==")
changes = resolver.explain_lock_change(reqs, {}, {"lib": Version.parse("1.2.0")})
for name, ch in changes.items():
    print(f"  {name}: {ch['old']} -> {ch['new']}")
    for cause in ch["caused_by"]:
        print(f"    caused_by: {cause}")

print()
print("== 不可满足解释（循环冲突夹具） ==")
conflict_repo = Repo.from_json_file("fixtures/repo_cycle_conflict.json")
try:
    Resolver(conflict_repo).resolve({"a": VersionSet.parse(">=1.0.0")})
except Unsatisfiable as exc:
    print(exc.explanation())

print()
print("== 最小反例与规范哈希 ==")
a = VersionSet.parse("[1.0.0,2.0.0)")
b = VersionSet.parse("[1.0.0,2.0.0]")
print(f"  [1.0.0,2.0.0) vs [1.0.0,2.0.0] -> {a.counterexample(b)}")
print(f"  规范哈希: {a.digest()}")
