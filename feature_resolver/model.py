"""Loading and validating package / build definitions from local JSON data."""

from __future__ import annotations

from . import expr as expr_mod
from .version import Version, VersionReq


class ModelError(ValueError):
    pass


def _require(cond, msg):
    if not cond:
        raise ModelError(msg)


class DepSpec:
    def __init__(self, package, req, features=(), default_features=True, disables=()):
        self.package = package
        self.req = req
        self.features = tuple(features)
        self.default_features = bool(default_features)
        self.disables = tuple(disables)

    @classmethod
    def from_json(cls, data, where):
        _require(isinstance(data, dict), f"{where}: dependency must be an object")
        _require("package" in data, f"{where}: dependency missing 'package'")
        _require("version" in data, f"{where}: dependency missing 'version'")
        return cls(
            package=data["package"],
            req=VersionReq.parse(str(data["version"])),
            features=data.get("features", []),
            default_features=data.get("default_features", True),
            disables=data.get("disable", []),
        )


class CondRule:
    def __init__(self, when_text, ast, enables=(), deps=()):
        self.when_text = when_text
        self.ast = ast
        self.enables = tuple(enables)
        self.deps = tuple(deps)


class FeatureDef:
    def __init__(self, name, enables=(), conditionals=(), deps=()):
        self.name = name
        self.enables = tuple(enables)
        self.conditionals = tuple(conditionals)
        self.deps = tuple(deps)


class PackageDef:
    def __init__(self, name, version, default_features=(), features=None):
        self.name = name
        self.version = version
        self.default_features = tuple(default_features)
        self.features = features or {}

    @property
    def key(self):
        return f"{self.name}@{self.version}"

    @classmethod
    def from_json(cls, data):
        where = f"package {data.get('name', '?')!r}"
        _require(isinstance(data, dict), "package entry must be an object")
        _require("name" in data, "package entry missing 'name'")
        _require("version" in data, f"{where}: missing 'version'")
        name = data["name"]
        version = Version.parse(str(data["version"]))
        defaults = list(data.get("default_features", []))
        features = {}
        for fname, fdata in data.get("features", {}).items():
            fwhere = f"{name}@{version}:{fname}"
            _require(isinstance(fdata, dict), f"{fwhere}: feature body must be an object")
            conditionals = []
            for i, c in enumerate(fdata.get("conditional", [])):
                cwhere = f"{fwhere} conditional[{i}]"
                _require("when" in c, f"{cwhere}: missing 'when'")
                ast = expr_mod.parse(c["when"])
                conditionals.append(
                    CondRule(
                        when_text=c["when"],
                        ast=ast,
                        enables=c.get("enables", []),
                        deps=[DepSpec.from_json(d, cwhere) for d in c.get("deps", [])],
                    )
                )
            features[fname] = FeatureDef(
                name=fname,
                enables=fdata.get("enables", []),
                conditionals=conditionals,
                deps=[DepSpec.from_json(d, fwhere) for d in fdata.get("deps", [])],
            )
        pkg = cls(name, version, defaults, features)
        pkg._validate()
        return pkg

    def _validate(self):
        for f in self.default_features:
            _require(f in self.features, f"{self.key}: unknown default feature {f!r}")
        for fname, fdef in self.features.items():
            targets = list(fdef.enables)
            for cond in fdef.conditionals:
                targets.extend(cond.enables)
                for ref in expr_mod.names(cond.ast):
                    _require(
                        ref in self.features,
                        f"{self.key}:{fname}: expression references unknown feature {ref!r}",
                    )
            for t in targets:
                _require(t in self.features, f"{self.key}:{fname}: enables unknown feature {t!r}")


class RootRequest:
    def __init__(self, package, req, enable=(), disable=(), default_features=True):
        self.package = package
        self.req = req
        self.enable = tuple(enable)
        self.disable = tuple(disable)
        self.default_features = bool(default_features)

    @classmethod
    def from_json(cls, data):
        _require(isinstance(data, dict), "root request must be an object")
        _require("package" in data, "root request missing 'package'")
        _require("version" in data, f"root for {data.get('package')!r}: missing 'version'")
        return cls(
            package=data["package"],
            req=VersionReq.parse(str(data["version"])),
            enable=data.get("enable", []),
            disable=data.get("disable", []),
            default_features=data.get("default_features", True),
        )


def load_document(data):
    """Parse a full build document: {'packages': [...], 'build': {'roots': [...]}}."""
    _require(isinstance(data, dict), "top-level document must be an object")
    packages = [PackageDef.from_json(p) for p in data.get("packages", [])]
    seen = set()
    for p in packages:
        _require(p.key not in seen, f"duplicate package definition {p.key}")
        seen.add(p.key)
    build = data.get("build", {})
    roots = [RootRequest.from_json(r) for r in build.get("roots", [])]
    return packages, roots
