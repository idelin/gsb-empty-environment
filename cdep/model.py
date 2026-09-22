"""Graph model and JSON loader for conditional dependency exposure analysis.

JSON schema::

    {
      "root": "app@1",
      "features": ["ssl", "opt"],
      "packages": [
        {"id": "app@1", "provides": ["main"],
         "exports": {"public":   ["pkg@2"],
                     "private":  ["pkg@3"],
                     "build":    ["tool@9"]},
         "rename": {"sym": "alias"},
         "hide": ["internal"]},
        {"id": "pkg@2", "provides": ["sym"],
         "dependencies": {"public": [{"to": "pkg@4", "when": "ssl"}]}}
      ]
    }

Each dependency entry may be a bare node id or {"to": ..., "when": <expr>,
"rename": {...}, "hide": [...]}.  Edge-local rename/hide applies only to the
export the dependency brings into the importer (i.e. the direct view); the
analyzer currently applies package-level rename/hide, which is what the
described semantics need, but edge data is preserved for queries.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

from .bdd import ROBDD, Expr, TRUE


@dataclass(frozen=True)
class Source:
    """Identity of a supplied symbol: package id, version, original name."""

    pkg_id: str
    version: str
    symbol: str

    def as_tuple(self) -> Tuple[str, str, str]:
        return (self.pkg_id, self.version, self.symbol)


@dataclass
class Package:
    id: str
    version: str
    provides: List[str] = field(default_factory=list)
    rename: Dict[str, str] = field(default_factory=dict)
    hide: List[str] = field(default_factory=list)
    raw: dict = field(default_factory=dict)


@dataclass
class Edge:
    kind: str  # "public" | "private" | "build"
    src: str
    dst: str
    cond: int = TRUE
    cond_text: str = "1"
    rename: Dict[str, str] = field(default_factory=dict)
    hide: List[str] = field(default_factory=list)

    def identity(self) -> Tuple[str, str, str]:
        return (self.kind, self.src, self.dst)


def split_node(ref: str) -> Tuple[str, str]:
    if "@" not in ref:
        return ref, ""
    pid, ver = ref.rsplit("@", 1)
    return pid, ver


class GraphSpec:
    def __init__(self, data: dict):
        self.data = data
        features: List[str] = list(data.get("features", []))
        self.bdd = ROBDD(features)
        self.expr = Expr(self.bdd)
        self.root: str = data["root"]
        self.packages: Dict[str, Package] = {}
        self.edges: List[Edge] = []
        self._load_packages()
        self._load_edges()
        if self.root not in self.packages:
            raise ValueError(f"root {self.root!r} is not a declared package")

    # -- loading -----------------------------------------------------------
    def _load_packages(self) -> None:
        for p in self.data.get("packages", []):
            ref = p["id"]
            pid, ver = split_node(ref)
            self.packages[ref] = Package(
                id=pid,
                version=ver,
                provides=list(p.get("provides", [])),
                rename=dict(p.get("rename", {})),
                hide=list(p.get("hide", [])),
                raw=p,
            )

    def _edge_bucket(self, p: dict, key: str) -> list:
        if key == "public":
            return list(p.get("dependencies", {}).get("public", []))
        return list(p.get("exports", {}).get(key, []))

    def _load_edges(self) -> None:
        for ref, pkg in self.packages.items():
            raw = pkg.raw
            deps = raw.get("dependencies", {})
            exports = raw.get("exports", {})
            # public edges may live under either "dependencies.public" or
            # "exports.public"; merge both.
            buckets = [
                ("public", list(deps.get("public", [])) + list(exports.get("public", []))),
                ("private", list(exports.get("private", []))),
                ("build", list(exports.get("build", []))),
            ]
            for kind, entries in buckets:
                for entry in entries:
                    self._add_edge_entry(kind, ref, entry)

    def _add_edge_entry(self, kind: str, src: str, entry) -> None:
        if isinstance(entry, str):
            dst, cond_text = entry, "1"
            rename, hide = {}, []
        else:
            dst = entry["to"]
            cond_text = str(entry.get("when", "1"))
            rename = dict(entry.get("rename", {}))
            hide = list(entry.get("hide", []))
        if dst not in self.packages:
            raise ValueError(f"edge {src} -> {dst} points at undeclared package")
        self.bdd.ensure_variables(_expr_features(cond_text, self.expr))
        cond = self.expr.parse(cond_text)
        self.edges.append(Edge(kind, src, dst, cond, cond_text, rename, hide))

    # -- io ----------------------------------------------------------------
    @classmethod
    def from_file(cls, path: str) -> "GraphSpec":
        with open(path, "r", encoding="utf-8") as fh:
            return cls(json.load(fh))

    @classmethod
    def from_string(cls, text: str) -> "GraphSpec":
        return cls(json.loads(text))


def _expr_features(text: str, expr: Expr) -> List[str]:
    """Best-effort scan of identifier tokens that are feature variables."""
    out: List[str] = []
    for tok in Expr._tokenize(str(text)):
        if tok and (tok[0].isalpha() or tok[0] == "_") and tok not in ("true", "false"):
            out.append(tok)
    return out
