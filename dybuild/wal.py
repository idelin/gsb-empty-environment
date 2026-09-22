"""Append-only local commit log (JSONL).

Every node execution is a transaction with three ordered commit points:

    begin -> graph -> cache -> publish* -> commit

* graph commit point: new dependency edges for the node
* cache commit point: the cache record (key, observations, versions)
* publish commit point: output artifacts atomically replaced
* commit marker: the transaction is visible; recovery rolls forward.

Records without a commit marker are aborted on recovery: replaced outputs are
restored from pre-images and staging is removed.
"""
from __future__ import annotations

import json
import os
from collections import OrderedDict
from typing import Iterator


class WriteAheadLog:
    def __init__(self, state_dir: str):
        self.dir = state_dir
        os.makedirs(self.dir, exist_ok=True)
        self.path = os.path.join(self.dir, "log.jsonl")
        self._fh = open(self.path, "a", encoding="utf-8")

    def append(self, entry: dict) -> None:
        self._fh.write(json.dumps(entry, sort_keys=True) + "\n")
        self._fh.flush()
        os.fsync(self._fh.fileno())

    def close(self) -> None:
        try:
            self._fh.close()
        except Exception:
            pass

    def transactions(self) -> "OrderedDict[str, list[dict]]":
        grouped: OrderedDict[str, list[dict]] = OrderedDict()
        if not os.path.exists(self.path):
            return grouped
        with open(self.path, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                entry = json.loads(line)
                grouped.setdefault(entry["txn"], []).append(entry)
        return grouped

    def rewrite_committed(self, keep: Iterator[dict]) -> None:
        tmp = self.path + ".compact"
        with open(tmp, "w", encoding="utf-8") as fh:
            for entry in keep:
                fh.write(json.dumps(entry, sort_keys=True) + "\n")
        os.replace(tmp, self.path)
