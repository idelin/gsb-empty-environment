# Capability Resolver

A small, dependency-free Python 3.11 tool that maps requested **capabilities**
to concrete **package implementations**. Inputs are local JSON files (no network
access). It is built with the standard library and tested with `unittest`.

## Problem model

- Many packages can provide the **same capability**.
- Each package has a semantic version `major.minor.patch` and may be a
  pre-release (`1.2.3-rc.1`).
- Packages may declare **mutual exclusion** via `conflicts_with`.
- Packages may constrain when they apply via `requires_target` /
  `excludes_target`.
- A package may declare `replaced_by`, forming an **alternative chain**: if the
  original cannot be used, its replacement inherits the capability.
- Each requested capability must end up with **exactly one** provider.

## Catalog schema (`catalog.json`)

```json
{
  "packages": [
    {
      "name": "A",
      "version": "2.0.0",
      "provides": ["X"],
      "conflicts_with": ["C"],
      "requires_target": [{"os": "linux"}],
      "excludes_target": [{"os": "windows"}],
      "replaced_by": "A-ng"
    }
  ]
}
```

- `name`, `version`, `provides` are required. A package reached only through a
  `replaced_by` chain may omit the capabilities it inherits.
- `conflicts_with`, `requires_target`, `excludes_target`, `replaced_by` are
  optional.
- Validation rejects duplicate package versions, unknown/inconsistent/self
  alternatives, and packages that provide nothing without being a replacement.

## Build request schema (`build.json`)

```json
{
  "requirements": [
    "X",
    "Y>=1.0.0",
    {"capability": "Z", "constraints": [">=1.0.0", "<2.0.0"]}
  ],
  "target": {"os": "linux", "arch": "amd64"}
}
```

Supported constraint operators: `==`, `!=`, `>`, `>=`, `<`, `<=`. A bare
version (`X<2.0.0` may also be written in an object) means exact equality.

## Stable decision rules

Candidates are evaluated in this documented order, so identical inputs always
produce identical outputs:

1. Drop candidates violating a target `requires`/`excludes` constraint.
2. Drop candidates whose version fails any requested version constraint.
3. Follow `replaced_by` chains; revisiting a package is a **cycle error**
   (a ring never recurses infinitely).
4. Prefer the highest semantic version; a release outranks a same-core
   pre-release (`1.0.0` > `1.0.0-rc.1`).
5. Skip providers that conflict with an already selected package and try the
   next provider, backtracking across capabilities when needed.
6. On equal version, prefer the lexicographically smallest provider name.
7. On equal provider and version, keep catalog declaration order.

When the preferred provider (e.g. `A`) conflicts, the resolver automatically
tries the other provider of the same capability (`B`).

## Usage

```bash
python3.11 -m capability_resolver --catalog examples/catalog.json \
                                  --build examples/build.json
```

Success prints the mapping to stdout and exits `0`. Any
validation/resolution error prints `error: ...` to stderr and exits `1`.

### Sample run

`examples/build.json` requests `X Y Z W Q TIE` on `linux/amd64`. `Y` selects
`C`, which conflicts with every `A`, so `X` falls back to `B`; `W` backtracks
from `E` (conflicts with `B`) to `F`; `Q` follows `G -> G2` and picks the
linux-only `G2@2.0.0`; `TIE` between equal versions picks `H`.

```json
"mapping": {
  "Q": {"package": "G2", "version": "2.0.0"},
  "TIE": {"package": "H", "version": "1.0.0"},
  "W": {"package": "F", "version": "1.0.0"},
  "X": {"package": "B", "version": "1.1.0"},
  "Y": {"package": "C", "version": "1.0.0"},
  "Z": {"package": "D", "version": "1.0.0"}
}
```

A cyclic catalog exits non-zero:

```text
$ python3.11 -m capability_resolver --catalog examples/catalog_cycle.json \
                                    --build examples/build_cycle.json
error: alternative chain forms a cycle: P -> Q -> R -> P   (exit=1)
```

## Tests

```bash
python3.11 -m unittest discover -s tests -v
```

28 tests cover version/pre-release ordering, constraint filtering, conflict
fallback and backtracking, alternative inheritance and cycle detection,
target requires/excludes, single-provider reuse, tie-breaking, determinism,
catalog validation, and end-to-end JSON/CLI behavior.

## Layout

- `capability_resolver/version.py` — semver parsing, comparison, constraints.
- `capability_resolver/models.py` — `Target`, `Requirement`, `Package`, `Catalog`.
- `capability_resolver/loader.py` — local JSON loading and validation.
- `capability_resolver/resolver.py` — backtracking resolver + decision policy.
- `capability_resolver/__main__.py` — CLI.
- `tests/test_resolver.py` — unittest suite.
- `examples/` — runnable catalog and build requests.
