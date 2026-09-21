# abicompat — local API compatibility impact analyzer

Computes the compatibility impact of upgrading packages from one version to
another, then propagates each breaking change backward along the **call
edges** of a resolved build graph until it reaches a **root** package. The
report gives concrete breaking paths (`root.symbol -> ... -> changed.symbol`)
and the affected call sites on those paths.

* Python 3.11, standard library only; no network access is performed.
* Package/version/symbol data and build input come from local JSON files.
* Versions are `major.minor.patch` with optional SemVer pre-release and
  build metadata (e.g. `1.9.0-rc.1+build.7`).
* Tests use `unittest`.

## Running

```bash
# human-readable report
python3.11 -m abicompat --packages examples/packages.json --build examples/build.json

# machine-readable report
python3.11 -m abicompat --packages examples/packages.json --build examples/build.json --format json

# CI gate: exit status 1 when a break reaches a root
python3.11 -m abicompat --packages examples/packages.json --build examples/build.json --check

# tests
python3.11 -m unittest discover -s tests -v
```

## Input files

### Package catalog (`--packages`)

Describes every exported symbol for every released version:

```json
{
  "type_parents": {"HttpError": ["Exception", "object"]},
  "packages": {
    "libcore": {
      "releases": {
        "1.4.2": {
          "symbols": {
            "parse": {
              "kind": "function",
              "params": [
                {"name": "text", "type": "str", "required": true},
                {"name": "strict", "type": "bool", "required": false}
              ],
              "return_type": "int",
              "markers": ["pure", "thread-safe"]
            },
            "STATUS": {"kind": "constant", "value_type": "int"},
            "_secret": {"kind": "function"}
          }
        }
      }
    }
  }
}
```

* `kind`: `function`, `class` (treated like a callable) or `constant`.
* Parameters are objects with `name`, `type` (default `object`) and
  `required` (default `true`); a bare string is also accepted.
* Callables use `return_type`; constants use `value_type`.
* `markers` is a set of behavior guarantees (e.g. `thread-safe`, `pure`).
* Symbols whose name starts with `_`, or that carry `"private": true`, are
  private. A symbol explicitly marked `"private": false` stays public.
* `type_parents` extends the built-in subtype lattice; every listed name is
  treated as a known type.

### Build input (`--build`)

Describes the versions resolved in one build and the statically observed
call edges between exported symbols:

```json
{
  "roots": ["app"],
  "nodes": {
    "app": {"old": "4.5.0", "new": "4.5.0"},
    "libhttp": {"old": "3.0.1", "new": "3.1.0"}
  },
  "edges": [
    {
      "from": "app.main",
      "to": "libhttp.fetch",
      "callsites": [{"file": "src/app/main.py", "line": 21, "note": "GET config"}]
    }
  ]
}
```

`from`/`to` are dotted `package.symbol` references. `roots` is optional;
when omitted, any node that never appears as an edge target is treated as a
root. Parallel edges between the same two symbols are merged (their call
sites union). Edges touching private symbols are ignored: private symbol
changes are never public breakage and private internals are not a public
dependency surface.

## Compatibility rules

A change is reported only when it is a **public breaking change** and a
root can reach the symbol through the modeled call graph.

| Change | Breaking? |
| --- | --- |
| Public symbol removed | yes |
| Public symbol hidden as private (`private: true`) | yes |
| Private symbol removed/changed/exposed | no |
| New symbol added | no |
| Symbol `kind` changed (`function`/`class`/`constant`) | yes |
| Parameter removed, or new parameter added as required | yes |
| Optional parameter added | no |
| Parameter order changed among surviving parameters | yes |
| Parameter (input) type **widened** (`int -> float`) | no — old arguments still accepted |
| Parameter (input) type **narrowed** (`float -> int`) | yes — some old arguments rejected |
| Return (output) type **narrowed** (`float -> int`) | no — old consumers still satisfied |
| Return (output) type **widened** (`int -> float`) | yes — old consumers may fail |
| Constant value type widened | yes; narrowed | no |
| Behavior marker removed | yes; added | no |

Widening/narrowing is decided against a subtype lattice (built-ins mirror
the Python hierarchy, e.g. `bool < int < float < complex < object`). Input
and output positions use opposite compatibility directions, as above.
Relationships involving types not present in the lattice (and not declared
via `type_parents`) are reported as unknown-type changes instead of being
silently accepted.

## Propagation

For each breaking change the analyzer walks incoming call edges from the
changed symbol toward every root. It enumerates simple paths only (cycles
are cut), keeps up to 20 paths per change, and reports:

* the impacted root,
* every concrete breaking path,
* the union of affected call sites along those paths.

A deleted symbol that no root symbol can reach — for example
`libcore.uncalled_legacy` in the sample — is listed under
"Not impacting any root" and does not produce a finding. A root's own API
changes have no modeled consumer above them and are likewise not findings.

## Bundled sample scenario

* `libcore 1.4.2 -> 2.0.0` (major): `parse` widens its parameter type
  (compatible), makes `strict` required (break), widens its return type
  `int -> object` (break), and drops the `thread-safe` marker (break).
  `encode` narrows a parameter and drops a marker, but nothing in any root
  reaches it. `STATUS` widens but is unused. `uncalled_legacy` is deleted
  but uncalled. The private `_secret` change is ignored.
* `libhttp 3.0.1 -> 3.1.0` (minor): no API change; it is only the
  propagation hop between `app` and `libcore`.
* `libmath 1.9.0-rc.1 -> 2.0.0` (major): the `x` parameter narrows
  `float -> int`, breaking `cli.run` directly (pre-release versions parse
  and order correctly).
* `app` reaches `libcore.parse` through two paths
  (`app.main -> libhttp.fetch -> libcore.parse` and
  `app.handle_body -> libhttp.decode_body -> libcore.parse`).

Expected result: 4 findings across roots `app` (3) and `cli` (1);
`encode`, `STATUS`, and `uncalled_legacy` are explicitly listed as not
impacting any root.
