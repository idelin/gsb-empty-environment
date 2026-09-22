"""Node specs, the task I/O context, observations and cache keys."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Sequence

from . import identity as I
from .workspace import Workspace

TaskFn = Callable[["TaskContext"], None]


@dataclass
class NodeSpec:
    name: str
    tool: str
    fn: TaskFn
    args: tuple = ()
    outputs: tuple[str, ...] = ()
    # statically declared input identities (also folded into the cache key)
    declared_inputs: tuple[str, ...] = ()
    # directory reads with a declared ordering rule
    dir_rules: dict[str, str] = field(default_factory=dict)
    env_whitelist: tuple[str, ...] = ()
    env: dict[str, str] = field(default_factory=dict)
    # names of other nodes whose outputs this node consumes (static edges)
    static_deps: tuple[str, ...] = ()


@dataclass(frozen=True)
class Observation:
    identity: str
    # ("file", digest) | ("dir", rule, fingerprint) | ("missing",)
    version: tuple

    @property
    def kind(self) -> str:
        return self.version[0]


class TaskContext:
    """I/O surface injected into task functions. Records every read."""

    def __init__(self, spec: NodeSpec, workspace: Workspace):
        self.spec = spec
        self.workspace = workspace
        self.observations: list[Observation] = []
        self._seen: set[tuple[str, tuple]] = set()
        self._output_allowed = {I.canon(o) for o in spec.outputs}
        self._staged: dict[str, bytes] = {}

    # -- file reads --------------------------------------------------------

    def read(self, path: str) -> bytes:
        identity = self.workspace.identity(path)
        token = self.workspace.token(path)
        self._record(identity, token)
        return self.workspace.read_file(path)

    def read_or(self, path: str, default: bytes = b"") -> bytes:
        """Read *path*, or *default* when absent. Records a negative dep."""
        identity = self.workspace.identity(path)
        token = self.workspace.token(path)
        self._record(identity, token)
        if token[0] == I.MISSING:
            return default
        return self.workspace.read_file(path)

    def exists(self, path: str) -> bool:
        identity = self.workspace.identity(path)
        token = self.workspace.token(path)
        self._record(identity, token)
        return token[0] != I.MISSING

    def list_dir(self, path: str = ".") -> list[tuple[str, str]]:
        rule = self.spec.dir_rules.get(self.workspace.identity(path), "lex")
        identity = self.workspace.identity(path)
        entries = self.workspace.list_dir(path, rule)
        token = (I.DIRECTORY, rule, I.directory_fingerprint(entries, rule))
        self._record(identity, token)
        return entries

    # -- outputs -----------------------------------------------------------

    def write_output(self, path: str, data: bytes | str) -> None:
        identity = I.canon(path)
        if identity not in self._output_allowed:
            raise PermissionError(f"node {self.spec.name!r} did not declare output {path!r}")
        self._staged[identity] = data.encode("utf-8") if isinstance(data, str) else bytes(data)

    # -- env / params ------------------------------------------------------

    def env_value(self, key: str) -> str | None:
        if key not in self.spec.env_whitelist:
            raise KeyError(f"env {key!r} is not in the whitelist of {self.spec.name!r}")
        return self.spec.env.get(key)

    # -- internals ---------------------------------------------------------

    def _record(self, identity: str, token: tuple) -> None:
        key = (identity, token)
        if key in self._seen:
            return
        self._seen.add(key)
        self.observations.append(Observation(identity, token))


# ---------------------------------------------------------------------------
# Cache key
# ---------------------------------------------------------------------------

def env_subset(spec: NodeSpec) -> dict[str, str]:
    return {k: spec.env.get(k, "") for k in sorted(spec.env_whitelist)}


def build_cache_key(spec: NodeSpec, declared_versions: dict[str, tuple]) -> str:
    material = {
        "tool": spec.tool,
        "args": list(spec.args),
        "env": env_subset(spec),
        "declared": [
            [I.canon(p), list(declared_versions.get(I.canon(p), ()))]
            for p in spec.declared_inputs
        ],
    }
    import json
    return "key:" + I.digest_text(json.dumps(material, sort_keys=True))
