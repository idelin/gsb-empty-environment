"""Versioned on-disk store with atomic publish.

Layout::

    <root>/CURRENT                 # single integer: committed version
    <root>/versions/<v>/dict.json      # physical merged dictionary
    <root>/versions/<v>/batch_*.jsonl  # re-encoded output batches
    <root>/versions/<v>/remap_*.json   # local-code -> global-code tables
    <root>/versions/<v>/ACCOUNTING.json
    <root>/tmp/<job>/              # in-flight merge state (journal, spill)

A new version becomes visible only when CURRENT is atomically replaced
(write temp file + fsync + os.replace).  Old versions stay on disk so
old batches keep decoding against the dictionary meanings they were
encoded with.
"""

import json
import os


def _fsync_dir(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _atomic_write(path, data: bytes):
    tmp = path + ".tmp"
    with open(tmp, "wb") as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, path)


class Store:
    def __init__(self, root):
        self.root = root
        os.makedirs(root, exist_ok=True)

    # -- versions ---------------------------------------------------------
    def current_version(self):
        path = os.path.join(self.root, "CURRENT")
        if not os.path.exists(path):
            return None
        with open(path, "r", encoding="utf-8") as f:
            return int(f.read().strip())

    def next_version(self):
        return (self.current_version() or 0) + 1

    def version_dir(self, version):
        return os.path.join(self.root, "versions", str(version))

    def tmp_dir(self, job_id):
        return os.path.join(self.root, "tmp", job_id)

    def commit_version(self, version):
        """Atomically make ``version`` the current one."""
        _atomic_write(
            os.path.join(self.root, "CURRENT"), str(version).encode("ascii")
        )
        _fsync_dir(self.root)

    # -- reads ------------------------------------------------------------
    def load_dict(self, version=None):
        version = version if version is not None else self.current_version()
        with open(
            os.path.join(self.version_dir(version), "dict.json"),
            "r",
            encoding="utf-8",
        ) as f:
            return json.load(f)

    def accounting(self, version=None):
        version = version if version is not None else self.current_version()
        with open(
            os.path.join(self.version_dir(version), "ACCOUNTING.json"),
            "r",
            encoding="utf-8",
        ) as f:
            return json.load(f)

    def batch_files(self, version=None):
        version = version if version is not None else self.current_version()
        vdir = self.version_dir(version)
        return sorted(
            os.path.join(vdir, n)
            for n in os.listdir(vdir)
            if n.startswith("batch_")
        )

    def iter_rows(self, version=None):
        """Decode every output batch in order; yields row tuples."""
        payload = self.load_dict(version)
        terms = {
            col: spec["terms"] for col, spec in payload["columns"].items()
        }
        for path in self.batch_files(version):
            with open(path, "r", encoding="utf-8") as f:
                header = json.loads(f.readline())
                schema = header["schema"]
                for line in f:
                    rec = json.loads(line)
                    row = []
                    for (name, typ), val in zip(schema, rec):
                        if val is None:
                            row.append(None)
                        elif typ == "str":
                            row.append(terms[name][val])
                        else:
                            row.append(val)
                    yield tuple(row)
