"""Dependency-closure mathematics and closure verification.

Two closures are required for a package *root*:

* **runtime closure** ``R``: the root plus everything reachable through
  ``run_requires`` edges — packages that must exist on the target platform.
* **host-build closure**: host build tools declared by any package in ``R``
  (``host_requires``), closed over *both* edge kinds, because a host tool has
  runtime needs of its own and may itself need build tools.

A snapshot may only be published when both closures are fully present and
their archive objects verify.

The algorithms are plain BFS least-fixed-points over a ``meta_of`` callable,
so the same math is reused by the independent audit calculator with no
repository machinery involved.
"""

from collections import deque

RUN = "run_requires"
HOST = "host_requires"
LABELS = (RUN, HOST)

METADATA_MISSING = "metadata-missing"
CONTENT_MISSING = "content-missing"
DIGEST_MISMATCH = "digest-mismatch"


def coord_key(coord):
    return (coord["name"], coord["version"], coord["platform"])


def coord_str(coord):
    return f"{coord['name']}=={coord['version']}@{coord['platform']}"


def least_fixed_point(roots, meta_of, labels):
    """BFS least fixed point over selected edge labels.

    ``meta_of(node)`` returns metadata or ``None``.  A node whose metadata is
    absent is still recorded as reachable (so callers can report it) but its
    outgoing edges cannot be followed.  Returns ``(reachable, paths)``; each
    path is a list that starts with the root and alternates edge labels and
    coordinates, e.g. ``[root, "run_requires", dep, "host_requires", tool]``.
    """
    reachable = {}
    paths = {}
    queue = deque()
    for root in roots:
        key = coord_key(root)
        if key not in reachable:
            reachable[key] = root
            paths[key] = [root]
            queue.append(root)
    while queue:
        node = queue.popleft()
        meta = meta_of(node)
        if meta is None:
            continue
        for label in labels:
            for dep in meta.get(label, ()):
                dep_key = coord_key(dep)
                if dep_key not in reachable:
                    reachable[dep_key] = dep
                    paths[dep_key] = paths[coord_key(node)] + [label, dep]
                    queue.append(dep)
    return reachable, paths


def format_path(path):
    """Render a structured path as ``root --label--> dep ...``."""
    parts = [coord_str(path[0])]
    i = 1
    while i < len(path):
        label = path[i]
        coord = path[i + 1]
        parts.append(f"--{label}-->")
        parts.append(coord_str(coord))
        i += 2
    return " ".join(parts)


class ClosureReport:
    """Result of :func:`evaluate_closures`."""

    def __init__(self, root, runtime_keys, host_seed_keys, host_keys,
                 reachable, paths, issues):
        self.root = root
        self.runtime_keys = set(runtime_keys)
        self.host_seed_keys = set(host_seed_keys)
        self.host_keys = set(host_keys)
        self.reachable_keys = set(reachable)
        self.paths = paths
        self.issues = issues

    @property
    def complete(self):
        return not self.issues


def evaluate_closures(root, resolve):
    """Evaluate both closures through a repository-backed ``resolve``.

    ``resolve(coord)`` returns one of:

    * ``("ok", meta)`` — metadata present and archive verified
    * ``("metadata-missing",)`` — coordinate not imported
    * ``("content-missing", meta)`` — metadata present, archive absent
    * ``("digest-mismatch", meta)`` — archive present but hash differs

    Returns a :class:`ClosureReport`; ``issues`` carry the root-to-missing
    path and one of the three kind constants.
    """
    status_cache = {}

    def status_of(node):
        key = coord_key(node)
        if key not in status_cache:
            status_cache[key] = resolve(node)
        return status_cache[key]

    def meta_of(node):
        status = status_of(node)
        if status[0] == "metadata-missing":
            return None
        return status[1]

    runtime, run_paths = least_fixed_point([root], meta_of, (RUN,))
    union, union_paths = least_fixed_point([root], meta_of, LABELS)

    host_seeds = set()
    for key, node in runtime.items():
        status = status_of(node)
        if status[0] == "ok":
            for tool in status[1].get(HOST, ()):
                host_seeds.add(coord_key(tool))

    issues = []
    seen = set()
    for key, node in union.items():
        kind = status_of(node)[0]
        if kind == "ok":
            continue
        issue = {
            "kind": kind,
            "coordinate": dict(node),
            "path": format_path(union_paths[key]),
        }
        dedup = (kind, key)
        if dedup not in seen:
            seen.add(dedup)
            issues.append(issue)

    issues.sort(key=lambda item: (item["kind"], coord_str(item["coordinate"])))
    return ClosureReport(
        root=root,
        runtime_keys=runtime,
        host_seed_keys=host_seeds,
        host_keys=set(union) - set(runtime),
        reachable=union,
        paths=union_paths,
        issues=issues,
    )
