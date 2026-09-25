"""Incremental multi-module compilation engine.

* Active definitions are selected per apply() from sources + condition flags.
* Each const gets a dependency fingerprint; unchanged fingerprints reuse the
  previous value without re-evaluating (see Engine.eval_count).
* Failed consts retain the previous good value; diagnostics are rebuilt from
  scratch every apply(), so toggled flags remove/add diagnostics exactly once.
* Failed batches never publish: require_clean=True validates in a staging area
  and leaves the last good snapshot untouched; cancellation works the same.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .diag import Diagnostic, Span
from .eval import (
    CancelToken,
    Cancelled,
    EnumVal,
    EvalError,
    OverflowPolicy,
    eval_expr,
)
from .parser import (
    ArrayLit,
    Bin,
    ConstItem,
    EnumItem,
    Index,
    IntLit,
    Neg,
    Ref,
    WhenItem,
    parse_module,
    sexpr,
)


@dataclass
class Entry:
    value: Any  # last good value (may be None when it never compiled cleanly)
    fingerprint: str | None
    status: str  # 'ok' | 'error'
    deps: tuple[str, ...] = ()
    flags: tuple[str, ...] = ()

    def to_json(self) -> dict:
        return {
            "value": value_to_json(self.value),
            "fingerprint": self.fingerprint,
            "status": self.status,
            "deps": list(self.deps),
            "flags": list(self.flags),
        }

    @staticmethod
    def from_json(data: dict) -> "Entry":
        return Entry(
            value_from_json(data["value"]),
            data["fingerprint"],
            data["status"],
            tuple(data["deps"]),
            tuple(data["flags"]),
        )


@dataclass
class Snapshot:
    entries: dict[str, Entry] = field(default_factory=dict)
    retired: dict[str, Entry] = field(default_factory=dict)

    def to_json(self) -> dict:
        return {
            "entries": {k: v.to_json() for k, v in sorted(self.entries.items())},
            "retired": {k: v.to_json() for k, v in sorted(self.retired.items())},
        }

    @staticmethod
    def from_json(data: dict) -> "Snapshot":
        return Snapshot(
            {k: Entry.from_json(v) for k, v in data["entries"].items()},
            {k: Entry.from_json(v) for k, v in data.get("retired", {}).items()},
        )


@dataclass
class ApplyResult:
    committed: bool
    diagnostics: list[Diagnostic]
    evaluated: list[str]
    reused: list[str]

    @property
    def ok(self) -> bool:
        return self.committed and not self.diagnostics


@dataclass
class _Def:
    item: ConstItem
    exported: bool
    gate_flags: tuple[str, ...]


class _ResolveErr(Exception):
    pass


def skey(module: str, name: str) -> str:
    return f"{module}.{name}"


def value_to_json(value: Any) -> Any:
    if isinstance(value, EnumVal):
        return {"$enum": [value.enum, value.member, value.value]}
    if isinstance(value, list):
        return [value_to_json(v) for v in value]
    return value


def value_from_json(data: Any) -> Any:
    if isinstance(data, dict) and "$enum" in data:
        enum, member, value = data["$enum"]
        return EnumVal(enum, member, int(value))
    if isinstance(data, list):
        return [value_from_json(v) for v in data]
    return data


def _walk_refs(node: object):
    if isinstance(node, Ref):
        yield node
    elif isinstance(node, Bin):
        yield from _walk_refs(node.left)
        yield from _walk_refs(node.right)
    elif isinstance(node, Neg):
        yield from _walk_refs(node.operand)
    elif isinstance(node, Index):
        yield from _walk_refs(node.target)
        yield from _walk_refs(node.index)
    elif isinstance(node, ArrayLit):
        for item in node.items:
            yield from _walk_refs(item)


def _resolve_ref(module, parts, defs, enum_defs, modules):
    """Return ('const', (mod, name)) or ('enum', (mod, enum), member).

    Qualified refs see exported consts only; unqualified refs resolve inside
    the current module (local or exported). Local and exported tables stay
    separate, so same-named constants in different modules never alias.
    """
    if len(parts) == 1:
        name = parts[0]
        if (module, name) in defs:
            return "const", (module, name)
        if (module, name) in enum_defs:
            raise _ResolveErr(f"enum '{name}' used as value; use '{name}.<member>'")
        raise _ResolveErr(f"unknown name '{name}'")

    if len(parts) == 2:
        head, tail = parts
        if head in modules:
            if (head, tail) in defs:
                if not defs[(head, tail)].exported:
                    raise _ResolveErr(
                        f"'{head}.{tail}' is local to module '{head}' and is not exported"
                    )
                return "const", (head, tail)
            if (head, tail) in enum_defs:
                raise _ResolveErr(f"enum '{head}.{tail}' used as value")
            raise _ResolveErr(f"module '{head}' has no exported constant '{tail}'")
        if (module, head) in enum_defs:
            return "enum", (module, head), tail
        raise _ResolveErr(f"unknown module or enum '{head}'")

    if len(parts) == 3:
        mod, enum_name, member = parts
        if mod not in modules:
            raise _ResolveErr(f"unknown module '{mod}'")
        if (mod, enum_name) not in enum_defs:
            raise _ResolveErr(f"module '{mod}' has no enum '{enum_name}'")
        return "enum", (mod, enum_name), member

    raise _ResolveErr(f"malformed reference {'.'.join(parts)!r}")


def _find_cycles(graph):
    """Iterative Tarjan. Returns (cyclic-member set, member->scc mapping)."""
    index_of = {}
    low = {}
    on_stack = set()
    stack = []
    counter = [0]
    member_of = {}

    for root in sorted(graph):
        if root in index_of:
            continue
        index_of[root] = low[root] = counter[0]
        counter[0] += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(sorted(graph[root])))]
        while work:
            node, it = work[-1]
            pushed = False
            for child in it:
                if child not in graph:
                    continue
                if child not in index_of:
                    index_of[child] = low[child] = counter[0]
                    counter[0] += 1
                    stack.append(child)
                    on_stack.add(child)
                    work.append((child, iter(sorted(graph[child]))))
                    pushed = True
                    break
                if child in on_stack:
                    low[node] = min(low[node], index_of[child])
            if pushed:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                low[parent] = min(low[parent], low[node])
            if low[node] == index_of[node]:
                scc = set()
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.add(w)
                    if w == node:
                        break
                if len(scc) > 1 or node in graph[node]:
                    for m in scc:
                        member_of[m] = scc
    return set(member_of), member_of


class _Ctx:
    def __init__(self, module, defs, entries, enum_values, enum_defs, modules):
        self.module = module
        self.defs = defs
        self.entries = entries
        self.enum_values = enum_values
        self.enum_defs = enum_defs
        self.modules = modules

    def resolve_ref(self, parts, span):
        result = _resolve_ref(self.module, parts, self.defs, self.enum_defs, self.modules)
        if result[0] == "const":
            entry = self.entries.get(skey(*result[1]))
            if entry is None or entry.status != "ok":
                raise EvalError("dependency-failed",
                                f"{skey(*result[1])} is unavailable", span)
            return entry.value
        _, ekey, member = result
        return EnumVal(ekey[1], member, self.enum_values[ekey][member])

    def def_expr_of(self, parts):
        try:
            result = _resolve_ref(self.module, parts, self.defs, self.enum_defs, self.modules)
        except _ResolveErr:
            return None
        if result[0] != "const":
            return None
        key = result[1]
        d = self.defs[key]
        return skey(*key), d.item.name_span, d.item.expr


class Engine:
    def __init__(self, overflow: str = "exact", bits: int = 64):
        self.overflow = overflow
        self.bits = bits
        self.sources: dict[str, str] = {}
        self.flags: dict[str, bool] = {}
        self.snapshot = Snapshot()
        self.diagnostics: list[Diagnostic] = []
        self.eval_count = 0
        self.last_evaluated: list[str] = []
        self.last_reused: list[str] = []
        self._parse_cache: dict[tuple[str, str], tuple] = {}

    # -- artifacts -------------------------------------------------

    def artifact(self, module: str, name: str):
        entry = self.snapshot.entries.get(skey(module, name))
        if entry is None or entry.value is None:
            raise KeyError(skey(module, name))
        return entry.value

    def artifacts(self) -> dict[str, Any]:
        return {k: e.value for k, e in sorted(self.snapshot.entries.items())
                if e.value is not None}

    def format_diagnostics(self) -> str:
        return "\n".join(d.format() for d in self.diagnostics)

    # -- public apply ----------------------------------------------

    def apply(self, *, sources: dict[str, str | None] | None = None,
              flags: dict[str, bool] | None = None,
              require_clean: bool = False,
              token: CancelToken | None = None) -> ApplyResult:
        """Validate a batch of changes, then publish atomically.

        sources maps module name -> new source, or None to remove a module.
        require_clean=True refuses to publish when staging produces any
        diagnostic. Cancelled staging never publishes.
        """
        new_sources = dict(self.sources)
        for name, src in (sources or {}).items():
            if src is None:
                new_sources.pop(name, None)
            else:
                new_sources[name] = src
        new_flags = dict(self.flags)
        new_flags.update(flags or {})

        count_before = self.eval_count
        try:
            snapshot, diags, evaluated, reused = self._compile(
                new_sources, new_flags, token)
        except Cancelled:
            self.eval_count = count_before
            raise

        if require_clean and diags:
            self.last_evaluated = evaluated
            self.last_reused = reused
            return ApplyResult(False, diags, evaluated, reused)

        self.sources = new_sources
        self.flags = new_flags
        self.snapshot = snapshot
        self.diagnostics = diags
        self.last_evaluated = evaluated
        self.last_reused = reused
        return ApplyResult(True, diags, evaluated, reused)

    # -- compile pipeline ------------------------------------------

    def _parse(self, module: str, source: str, diags: list[Diagnostic]):
        digest = hashlib.sha256(source.encode("utf-8")).hexdigest()
        cache_key = (module, digest)
        if cache_key not in self._parse_cache:
            self._parse_cache[cache_key] = parse_module(source, module)
        ast, parse_diags = self._parse_cache[cache_key]
        diags.extend(parse_diags)
        return ast

    def _collect(self, items, module, gate, flags, defs, enum_defs, diags):
        for item in items:
            if isinstance(item, WhenItem):
                if item.cond.eval(flags):
                    self._collect(
                        item.items, module,
                        gate | frozenset(item.cond.flags_used()),
                        flags, defs, enum_defs, diags,
                    )
            elif isinstance(item, ConstItem):
                key = (module, item.name)
                if key in defs:
                    diags.append(Diagnostic(
                        module, item.name_span, "duplicate-definition",
                        f"duplicate definition of constant '{item.name}'"))
                    continue
                defs[key] = _Def(item, item.exported, tuple(sorted(gate)))
            elif isinstance(item, EnumItem):
                key = (module, item.name)
                if key in enum_defs:
                    diags.append(Diagnostic(
                        module, item.name_span, "duplicate-definition",
                        f"duplicate definition of enum '{item.name}'"))
                    continue
                enum_defs[key] = item

    def _eval_enum_expr(self, node, values):
        if isinstance(node, IntLit):
            return node.value
        if isinstance(node, Ref) and len(node.parts) == 1 and node.parts[0] in values:
            if values[node.parts[0]] is None:
                raise EvalError("bad-enum-expr",
                                f"enum member '{node.parts[0]}' has no usable value",
                                node.span)
            return values[node.parts[0]]
        if isinstance(node, Neg):
            return -self._eval_enum_expr(node.operand, values)
        if isinstance(node, Bin) and node.op in ("+", "-", "*"):
            left = self._eval_enum_expr(node.left, values)
            right = self._eval_enum_expr(node.right, values)
            if node.op == "+":
                return left + right
            if node.op == "-":
                return left - right
            return left * right
        raise EvalError(
            "bad-enum-expr",
            "enum member expression may only use literals, earlier members and +,-,*",
            getattr(node, "span", None),
        )

    def _eval_enum(self, item: EnumItem, module: str, policy: OverflowPolicy,
                   diags: list[Diagnostic]):
        values: dict[str, int | None] = {}
        next_value = 0
        seen: set[str] = set()
        for member in item.members:
            if member.name in seen:
                diags.append(Diagnostic(
                    module, member.name_span, "duplicate-definition",
                    f"duplicate enum member '{member.name}'"))
                continue
            seen.add(member.name)
            if member.expr is None:
                value = next_value
            else:
                try:
                    value = self._eval_enum_expr(member.expr, values)
                    value = policy.apply(value, member.expr.span)
                except EvalError as exc:
                    diags.append(Diagnostic(module, exc.span, exc.code, exc.message, exc.chain))
                    values[member.name] = None
                    continue
            values[member.name] = value
            next_value = value + 1
        return values

    def _fingerprint(self, d: _Def, dep_fps, enum_deps, flags, policy) -> str:
        h = hashlib.sha256()
        h.update(sexpr(d.item.expr).encode("utf-8"))
        for dep in sorted(dep_fps):
            h.update(b"|dep:")
            h.update(dep.encode())
            h.update(b"=")
            h.update(dep_fps[dep].encode())
        for key in sorted(enum_deps):
            h.update(b"|enum:")
            h.update(repr((key, enum_deps[key])).encode())
        for flag in d.gate_flags:
            h.update(b"|flag:")
            h.update(flag.encode())
            h.update(b"=")
            h.update(b"1" if flags.get(flag, False) else b"0")
        h.update(f"|policy:{policy.mode}:{policy.bits}".encode())
        return h.hexdigest()

    def _compile(self, sources, flags, token: CancelToken | None):
        diags: list[Diagnostic] = []
        parsed = {}
        for module in sorted(sources):
            ast = self._parse(module, sources[module], diags)
            if ast is not None:
                parsed[module] = ast
        modules = set(sources)

        defs: dict[tuple[str, str], _Def] = {}
        enum_defs: dict[tuple[str, str], EnumItem] = {}
        for module in sorted(parsed):
            self._collect(parsed[module].items, module, frozenset(),
                          flags, defs, enum_defs, diags)

        policy = OverflowPolicy(self.overflow, self.bits)

        enum_values = {}
        for key in sorted(enum_defs):
            module, enum_name = key
            enum_values[key] = self._eval_enum(enum_defs[key], module, policy, diags)

        # static name resolution -> dependency graph + enum dependencies
        graph: dict[tuple[str, str], set] = {}
        enum_deps_map: dict[tuple[str, str], dict] = {}
        static_errors: dict[tuple[str, str], list[tuple[str, Span]]] = {}
        for key in sorted(defs):
            module, _ = key
            deps = set()
            enum_deps = {}
            errors = []
            for ref in _walk_refs(defs[key].item.expr):
                try:
                    result = _resolve_ref(module, ref.parts, defs, enum_defs, modules)
                except _ResolveErr as exc:
                    errors.append((str(exc), ref.span))
                    continue
                if result[0] == "const":
                    deps.add(result[1])
                else:
                    _, ekey, member = result
                    value = enum_values.get(ekey, {}).get(member)
                    if value is None:
                        errors.append(
                            (f"enum member '{ekey[1]}.{member}' has no usable value",
                             ref.span))
                    else:
                        enum_deps[(ekey[0], ekey[1], member)] = value
            graph[key] = deps
            enum_deps_map[key] = enum_deps
            if errors:
                static_errors[key] = errors

        cycle_members, sccs = _find_cycles(graph)

        # deterministic topological layering; leftovers sit in/behind cycles
        done = set()
        order = []
        remaining = set(defs)
        while remaining:
            ready = sorted(k for k in remaining if graph[k] <= done)
            if not ready:
                break
            order.extend(ready)
            done.update(ready)
            remaining.difference_update(ready)
        order.extend(sorted(remaining))

        entries: dict[str, Entry] = {}
        evaluated: list[str] = []
        reused: list[str] = []

        def fail(key, code, message, span, chain=()):
            d = defs[key]
            diags.append(Diagnostic(key[0], span, code, message, tuple(chain)))
            old = self.snapshot.entries.get(skey(*key))
            entries[skey(*key)] = Entry(
                old.value if old is not None else None,
                None,
                "error",
                tuple(sorted(skey(*dep) for dep in graph[key])),
                d.gate_flags,
            )

        for key in order:
            if token is not None:
                token.throw_if_cancelled()
            d = defs[key]

            if key in static_errors:
                for message, span in static_errors[key]:
                    fail(key, "name-error", message, span)
                continue

            if key in cycle_members:
                scc = sccs[key]
                ordered = sorted(scc)
                ordered = sorted(ordered, key=lambda m: 0 if m == key else 1)
                path = " -> ".join([skey(*m) for m in ordered] + [skey(*key)])
                chain = [(skey(*m), defs[m].item.name_span) for m in ordered if m != key]
                fail(key, "cycle", f"definition cycle: {path}",
                     d.item.name_span, chain)
                continue

            failed_dep = next(
                (dep for dep in sorted(graph[key])
                 if (e := entries.get(skey(*dep))) is None or e.status != "ok"),
                None,
            )
            if failed_dep is not None:
                fail(key, "dependency-failed",
                     f"depends on failed constant {skey(*failed_dep)}",
                     d.item.name_span,
                     [(skey(*failed_dep), defs[failed_dep].item.name_span)])
                continue

            dep_fps = {skey(*dep): entries[skey(*dep)].fingerprint for dep in graph[key]}
            fp = self._fingerprint(d, dep_fps, enum_deps_map[key], flags, policy)
            old = self.snapshot.entries.get(skey(*key))
            if old is not None and old.status == "ok" and old.fingerprint == fp:
                entries[skey(*key)] = Entry(
                    old.value, fp, "ok", tuple(sorted(dep_fps)), d.gate_flags)
                reused.append(skey(*key))
                continue

            self.eval_count += 1
            ctx = _Ctx(key[0], defs, entries, enum_values, enum_defs, modules)
            try:
                value = eval_expr(d.item.expr, ctx, policy, token)
            except EvalError as exc:
                fail(key, exc.code, exc.message, exc.span, exc.chain)
                continue
            entries[skey(*key)] = Entry(
                value, fp, "ok", tuple(sorted(dep_fps)), d.gate_flags)
            evaluated.append(skey(*key))

        retired = dict(self.snapshot.retired)
        for gone in list(retired):
            if gone in entries:
                del retired[gone]
        for old_key, old_entry in self.snapshot.entries.items():
            if old_key not in entries:
                retired[old_key] = old_entry

        diags.sort(key=lambda dg: (dg.module,
                                   dg.span.start if dg.span else -1,
                                   dg.code, dg.message))
        return Snapshot(entries, retired), diags, evaluated, reused

    # -- checkpoints -----------------------------------------------

    def save_checkpoint(self, path: str) -> None:
        data = {
            "version": 1,
            "overflow": self.overflow,
            "bits": self.bits,
            "sources": self.sources,
            "flags": self.flags,
            "eval_count": self.eval_count,
            "snapshot": self.snapshot.to_json(),
            "diagnostics": [d.to_json() for d in self.diagnostics],
        }
        Path(path).write_text(json.dumps(data, indent=2, ensure_ascii=False), encoding="utf-8")

    @classmethod
    def load_checkpoint(cls, path: str) -> "Engine":
        data = json.loads(Path(path).read_text(encoding="utf-8"))
        engine = cls(data["overflow"], data["bits"])
        engine.sources = dict(data["sources"])
        engine.flags = dict(data["flags"])
        engine.eval_count = int(data.get("eval_count", 0))
        engine.snapshot = Snapshot.from_json(data["snapshot"])
        engine.diagnostics = [Diagnostic.from_json(d) for d in data.get("diagnostics", [])]
        return engine
