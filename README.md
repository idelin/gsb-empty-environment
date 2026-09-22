# Capability Package Resolver

A Python 3.11, standard-library-only resolver for local package catalogs. Multiple packages can provide the same capability, and the resolver chooses one implementation using version constraints, prerelease policy, target constraints, mutual-exclusion declarations, dependency backtracking, and deterministic tie-breaks.

## Files

- `capability_resolver.py` — version parsing, local JSON loading, backtracking resolver, and CLI.
- `examples/packages.json` — local catalog containing `impl-a` and `impl-b` providers for `capability-x`.
- `examples/build.json` — sample build request.
- `examples/sample-output.json` — saved sample result.
- `tests/test_resolver.py` — `unittest` coverage.

## Input Format

Package catalog:

```json
{
  "packages": [
    {
      "name": "impl-b",
      "version": "1.8.0",
      "provides": ["capability-x"],
      "requires": ["core-util>=1.2.0"],
      "conflicts": ["other-package"],
      "target": {"os": "linux", "arch": "amd64"}
    }
  ]
}
```

Build request:

```json
{
  "requirements": ["capability-x>=1.0.0", {"capability": "service-c"}],
  "target": {"os": "linux", "arch": "amd64"},
  "allow_prerelease": false
}
```

Rules:

- Versions use `major.minor.patch` with optional `-prerelease` and ignored `+build.metadata`.
- Operators are `==`, `=`, `>=`, `<=`, `>`, `<`; comma-separated constraints are AND-ed.
- A package always provides its own name in addition to names in `provides`.
- `conflicts` is symmetric at resolution time: either side declaring the other is rejected.
- Every package target key must equal the corresponding build target key.
- Prereleases are excluded unless `allow_prerelease` is true for the build/requirement or a constraint names a prerelease.
- No network access is performed.

## Stable Decision Rules

1. Discard implementations that fail accumulated version constraints, prerelease policy, or target constraints.
2. Choose candidates in semantic-version descending order; a prerelease ranks below its final release.
3. For equal versions, choose the lexicographically smallest package name.
4. Reject a candidate that conflicts with a selected package and continue with the next candidate.
5. Reject a solution that assigns two selected package names to one capability, enforcing a single provider.
6. Process requirements and dependencies by capability name so identical input always produces identical output.
7. Track active capabilities and packages; a path that revisits one fails instead of recursing forever, then earlier choices backtrack.

## Run

```sh
python3 capability_resolver.py examples/packages.json examples/build.json
```

Optional output file:

```sh
python3 capability_resolver.py examples/packages.json examples/build.json -o result.json
```

The sample first tries higher-priority `impl-a` for `capability-x`; after `service-c` selects a package mutually exclusive with `impl-a`, global backtracking selects `impl-b`, which supports the Linux/amd64 target.

## Test

```sh
python3 -m unittest discover -s tests -v
```

Latest local result:

```text
Ran 13 tests in 0.002s

OK
```
