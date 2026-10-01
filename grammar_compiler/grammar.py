"""Grammar front-end: namespaced modules, conditions, transactions, compile."""
from __future__ import annotations

import copy
from contextlib import contextmanager

from .engine import IncrementalAnalysis
from .snapshot import Snapshot
from .symbols import Production, Sym
from .table import build_table


class Module:
    """Namespace for nonterminals; same-named symbols in different modules
    are distinct and never merged."""

    def __init__(self, grammar, name):
        self.grammar = grammar
        self.name = name
        self.imports = {}       # alias -> module name
        self.pids = []

    def import_module(self, target, alias=None):
        if target not in self.grammar.modules:
            raise KeyError(f"unknown module {target!r}")
        self.imports[alias or target] = target

    def sym(self, name):
        return Sym("N", name, self.name)

    def _resolve(self, ref):
        if isinstance(ref, Sym):
            return ref
        if "." in ref:
            mod, name = ref.split(".", 1)
            mod = self.imports.get(mod, mod)
            if mod not in self.grammar.modules:
                raise KeyError(
                    f"unknown module {mod!r} referenced from {self.name!r}")
            return Sym("N", name, mod)
        if ref in self.grammar.terminals:
            return Sym("T", ref)
        return Sym("N", ref, self.name)

    def add(self, lhs, rhs=(), condition=None):
        g = self.grammar
        lhs_sym = lhs if isinstance(lhs, Sym) else Sym("N", lhs, self.name)
        if lhs_sym.is_terminal:
            raise ValueError("lhs must be a nonterminal")
        rhs_syms = tuple(self._resolve(r) for r in rhs)
        pid = g._next_pid
        g._next_pid += 1
        prod = Production(pid, lhs_sym, rhs_syms, condition)
        g.analysis.add_production(prod)
        self.pids.append(pid)
        return pid

    def remove(self, pid):
        self.grammar.remove_production(pid)


class Grammar:
    def __init__(self):
        self.analysis = IncrementalAnalysis()
        self.terminals = set()
        self.modules = {}
        self._next_pid = 1
        self._start = None

    def add_terminals(self, *names):
        self.terminals.update(names)

    def module(self, name):
        if name not in self.modules:
            self.modules[name] = Module(self, name)
        return self.modules[name]

    def set_start(self, module, name):
        if module not in self.modules:
            raise KeyError(f"unknown module {module!r}")
        self._start = Sym("N", name, module)
        self.analysis.set_start(self._start)

    @property
    def start(self):
        return self._start

    def enable_condition(self, condition):
        self.analysis.enable_condition(condition)

    def disable_condition(self, condition):
        self.analysis.disable_condition(condition)

    def remove_production(self, pid):
        self.analysis.remove_production(pid)
        for mod in self.modules.values():
            if pid in mod.pids:
                mod.pids.remove(pid)
                break

    @contextmanager
    def transaction(self):
        """All-or-nothing mutation: any exception rolls back every change."""
        saved_analysis = copy.deepcopy(self.analysis)
        saved_terminals = set(self.terminals)
        saved_next_pid = self._next_pid
        saved_start = self._start
        saved_modules = {name: (list(m.pids), dict(m.imports))
                         for name, m in self.modules.items()}
        try:
            yield self
        except Exception:
            self.analysis = saved_analysis
            self.terminals = saved_terminals
            self._next_pid = saved_next_pid
            self._start = saved_start
            for name in list(self.modules):
                if name not in saved_modules:
                    del self.modules[name]
            for name, (pids, imports) in saved_modules.items():
                self.modules[name].pids = pids
                self.modules[name].imports = imports
            raise

    def compile(self):
        a = self.analysis
        prods = tuple(p for _, p in sorted(a.productions.items()))
        active = [p for p in prods
                  if p.condition is None
                  or p.condition in a.enabled_conditions]
        nullable = frozenset(a.nullable)
        first = {k: frozenset(v) for k, v in a.first.items()}
        follow = {k: frozenset(v) for k, v in a.follow.items()}
        table, conflicts = build_table(set(nullable), first, follow,
                                       active, self._start)
        return Snapshot(
            revision=a.revision,
            start=self._start,
            productions=prods,
            enabled_conditions=frozenset(a.enabled_conditions),
            nullable=nullable,
            first=tuple(sorted(first.items())),
            follow=tuple(sorted(follow.items())),
            table=tuple(sorted((k[0], k[1], v) for k, v in table.items())),
            conflicts=conflicts)
