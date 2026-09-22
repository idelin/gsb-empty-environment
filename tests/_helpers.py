from __future__ import annotations

import io
import json
import os
import shutil
import stat
import tempfile
import zipfile
from typing import Iterable

from reproduce_compare.archive import Budget
from reproduce_compare.compare import compare_trees
from reproduce_compare.evidence import build_bundle
from reproduce_compare.rules import load_rules
from reproduce_compare.verify import verify_bundle


def make_zip(members: Iterable[tuple[str, bytes, tuple[int, ...] | None, int | None, str | None]]) -> bytes:
    """members: (name, data, date_time, unix_mode, symlink_target)"""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        for name, data, ts, mode, symlink in members:
            info = zipfile.ZipInfo(name, date_time=ts or (1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            if symlink is not None:
                payload = symlink.encode("utf-8")
                info.external_attr = (stat.S_IFLNK | 0o777) << 16
                zf.writestr(info, payload)
                continue
            if name.endswith("/"):
                info.external_attr = (stat.S_IFDIR | (mode or 0o755)) << 16
                zf.writestr(info, b"")
                continue
            info.external_attr = (stat.S_IFREG | (mode or 0o644)) << 16
            zf.writestr(info, data)
    return buf.getvalue()


class Workspace:
    def __init__(self):
        self.root = tempfile.mkdtemp(prefix="rcmp-")
        self.old = os.path.join(self.root, "old")
        self.new = os.path.join(self.root, "new")
        os.makedirs(self.old)
        os.makedirs(self.new)

    def cleanup(self):
        shutil.rmtree(self.root, ignore_errors=True)

    def write_old(self, rel, data):
        path = os.path.join(self.old, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True) if os.path.dirname(rel) else None
        with open(path, "wb") as fh:
            fh.write(data)

    def write_new(self, rel, data):
        path = os.path.join(self.new, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True) if os.path.dirname(rel) else None
        with open(path, "wb") as fh:
            fh.write(data)

    def symlink_old(self, rel, target):
        path = os.path.join(self.old, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True) if os.path.dirname(rel) else None
        os.symlink(target, path)

    def symlink_new(self, rel, target):
        path = os.path.join(self.new, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True) if os.path.dirname(rel) else None
        os.symlink(target, path)

    def chmod_old(self, rel, mode):
        os.chmod(os.path.join(self.old, rel), mode, follow_symlinks=False)

    def chmod_new(self, rel, mode):
        os.chmod(os.path.join(self.new, rel), mode, follow_symlinks=False)


def run_case(ws: Workspace, rules_document=None, budget=None):
    ruleset = load_rules(rules_document)
    result = compare_trees(ws.old, ws.new, ruleset, budget or Budget())
    bundle = build_bundle(result, ruleset)
    verdict = verify_bundle(bundle, ws.old, ws.new, budget or Budget())
    return ruleset, result, bundle, verdict
