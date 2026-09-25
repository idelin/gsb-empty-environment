"""Merge engine: combine batches that use different local dictionaries.

Pipeline (resumable, journalled per batch):

1. collect  -- scan batches, validate codes (errors carry batch/row),
               gather referenced terms per string column.  The term set
               is bounded by the memory budget and spills sorted runs to
               disk; runs are k-way merged at the end.
2. encode   -- assign global codes deterministically (sorted terms), build
               per-batch remap tables (local code -> global code, -1 for
               unreferenced terms, which are thereby reclaimed), and
               re-encode rows.  Row buffers flush to disk when the budget
               is exceeded.
3. publish  -- copy dict/batches/remaps into a new version directory and
               atomically flip CURRENT.  A crash before the flip leaves
               the old version untouched and the job resumable.

Cancellation (via CancelController or a progress callback) persists the
journal; re-running ``merge`` with the same job id resumes where it
stopped and yields exactly the same result as an uninterrupted run.
"""

import heapq
import json
import os
import shutil
from dataclasses import dataclass, field

from .batch import STR
from .errors import MergeCancelled, MergeError
from .store import Store, _fsync_dir


class CancelController:
    def __init__(self):
        self.cancelled = False

    def cancel(self):
        self.cancelled = True


@dataclass
class MergeConfig:
    memory_budget_bytes: int = 1 << 20
    controller: CancelController = field(default_factory=CancelController)
    progress_callback: object = None  # called with (phase, index) per batch


@dataclass
class MergeResult:
    version: int
    batches: int
    nrows: int
    accounting: dict


class _TermCollector:
    """Bounded-memory set of terms; spills sorted runs to disk."""

    def __init__(self, spill_dir, budget_bytes, prefix):
        self._spill_dir = spill_dir
        self._budget = max(256, budget_bytes)
        self._prefix = prefix
        self._terms = set()
        self._est = 0
        self.runs = []
        self.spill_bytes = 0
        self._counter = 0

    def add(self, value):
        if value in self._terms:
            return
        self._terms.add(value)
        self._est += len(value.encode("utf-8")) + 24
        if self._est >= self._budget:
            self.flush()

    def flush(self):
        if not self._terms:
            return
        self._counter += 1
        path = os.path.join(
            self._spill_dir, f"terms_{self._prefix}_{self._counter:05d}.jsonl"
        )
        data = "".join(
            json.dumps(t, ensure_ascii=False) + "\n" for t in sorted(self._terms)
        )
        with open(path, "w", encoding="utf-8") as f:
            f.write(data)
        self.spill_bytes += len(data.encode("utf-8"))
        self.runs.append(path)
        self._terms.clear()
        self._est = 0

    def finalize(self):
        """Return the deterministic (sorted) unique term list."""
        self.flush()
        streams = [open(p, "r", encoding="utf-8") for p in self.runs]
        try:
            gens = ((json.loads(line) for line in s) for s in streams)
            out, prev = [], object()
            for term in heapq.merge(*gens):
                if term != prev:
                    out.append(term)
                    prev = term
            return out
        finally:
            for s in streams:
                s.close()

    def state(self):
        return {
            "runs": list(self.runs),
            "spill_bytes": self.spill_bytes,
            "counter": self._counter,
        }

    def restore(self, state):
        self.runs = list(state["runs"])
        self.spill_bytes = state["spill_bytes"]
        self._counter = state["counter"]


class _Journal:
    def __init__(self, path):
        self.path = path
        self.state = {
            "phase": "collect",
            "collect_next": 0,
            "encode_next": 0,
            "collectors": {},
            "out_files": [],
            "remap_files": [],
            "remap_bytes": 0,
            "output_bytes": 0,
            "version": None,
        }
        if os.path.exists(path):
            with open(path, "r", encoding="utf-8") as f:
                self.state.update(json.load(f))

    def save(self):
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(self.state, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, self.path)


class Merger:
    def __init__(self, store: Store, config: MergeConfig = None):
        self.store = store
        self.config = config or MergeConfig()
        self.fault_before_commit = None  # test hook: callable raising

    # -- helpers ----------------------------------------------------------
    def _check_cancel(self):
        if self.config.controller.cancelled:
            raise MergeCancelled()

    def _progress(self, phase, index):
        cb = self.config.progress_callback
        if cb is not None:
            cb(phase, index)

    @staticmethod
    def _validate_schema(batch, schema, index):
        if batch.schema != schema:
            raise MergeError(index, 0, -1, "schema mismatch")

    # -- main entry -------------------------------------------------------
    def merge(self, batches, job_id="job"):
        if not batches:
            raise ValueError("nothing to merge")
        schema = batches[0].schema
        str_cols = [name for name, typ in schema if typ == STR]

        tmpdir = self.store.tmp_dir(job_id)
        os.makedirs(tmpdir, exist_ok=True)
        journal = _Journal(os.path.join(tmpdir, "journal.json"))
        state = journal.state
        budget = self.config.memory_budget_bytes

        collectors = {}
        for col in str_cols:
            c = _TermCollector(tmpdir, budget, col)
            if col in state["collectors"]:
                c.restore(state["collectors"][col])
            collectors[col] = c

        # ---- phase 1: collect referenced terms --------------------------
        if state["phase"] == "collect":
            for i in range(state["collect_next"], len(batches)):
                self._check_cancel()
                batch = batches[i]
                self._validate_schema(batch, schema, i)
                for col in str_cols:
                    d = batch.dicts[col]
                    for r in range(batch.nrows):
                        if batch.is_null(col, r):
                            continue
                        code = batch.values[col][r]
                        if code < 0 or code >= len(d):
                            raise MergeError(
                                i, r, code, "code out of range for local "
                                "dictionary"
                            )
                        collectors[col].add(d.decode(code))
                    collectors[col].flush()  # durable per-batch checkpoint
                    state["collectors"][col] = collectors[col].state()
                state["collect_next"] = i + 1
                journal.save()
                self._progress("collect", i)
            state["phase"] = "encode"
            journal.save()

        # deterministic global dictionaries (sorted terms)
        global_terms = {c: collectors[c].finalize() for c in str_cols}
        global_index = {
            c: {t: k for k, t in enumerate(ts)}
            for c, ts in global_terms.items()
        }

        # ---- phase 2: re-encode -----------------------------------------
        if state["version"] is None:
            state["version"] = self.store.next_version()
            journal.save()
        for i in range(state["encode_next"], len(batches)):
            self._check_cancel()
            batch = batches[i]
            remaps = {}
            for col in str_cols:
                d = batch.dicts[col]
                remap = []
                for code in range(len(d)):
                    gi = global_index[col].get(d.decode(code))
                    remap.append(gi if gi is not None else -1)  # reclaimed
                remaps[col] = remap

            remap_path = os.path.join(tmpdir, f"remap_{i:05d}.json")
            payload = json.dumps(
                {"batch": i, "columns": remaps}, ensure_ascii=False
            ).encode("utf-8")
            with open(remap_path, "wb") as f:
                f.write(payload)
            state["remap_bytes"] += len(payload)
            state["remap_files"].append(remap_path)

            out_path = os.path.join(tmpdir, f"batch_{i:05d}.jsonl")
            state["output_bytes"] += self._write_batch(
                out_path, batch, remaps, schema, budget
            )
            state["out_files"].append(out_path)
            state["encode_next"] = i + 1
            journal.save()
            self._progress("encode", i)

        # ---- phase 3: atomic publish ------------------------------------
        result = self._publish(job_id, state, global_terms, batches)
        shutil.rmtree(tmpdir, ignore_errors=True)
        return result

    # -- output writing ---------------------------------------------------
    @staticmethod
    def _write_batch(path, batch, remaps, schema, budget):
        budget = max(256, budget)
        with open(path, "w", encoding="utf-8") as f:
            header = {"schema": schema, "nrows": batch.nrows}
            f.write(json.dumps(header, ensure_ascii=False) + "\n")
            buf, est = [], 0
            for ri in range(batch.nrows):
                rec = []
                for name, typ in schema:
                    if batch.is_null(name, ri):
                        rec.append(None)
                    elif typ == STR:
                        rec.append(remaps[name][batch.values[name][ri]])
                    else:
                        rec.append(batch.values[name][ri])
                line = json.dumps(rec, ensure_ascii=False) + "\n"
                buf.append(line)
                est += len(line)
                if est >= budget:  # spill row buffer to disk
                    f.write("".join(buf))
                    buf, est = [], 0
            if buf:
                f.write("".join(buf))
        return os.path.getsize(path)

    def _publish(self, job_id, state, global_terms, batches):
        version = state["version"]
        vdir = self.store.version_dir(version)
        os.makedirs(vdir, exist_ok=True)

        dict_payload = json.dumps(
            {
                "version": version,
                "columns": {
                    col: {
                        "terms": terms,
                        "term_ids": list(range(len(terms))),
                    }
                    for col, terms in global_terms.items()
                },
            },
            ensure_ascii=False,
            sort_keys=True,
        ).encode("utf-8")
        with open(os.path.join(vdir, "dict.json"), "wb") as f:
            f.write(dict_payload)

        for src in state["out_files"] + state["remap_files"]:
            shutil.copy2(src, os.path.join(vdir, os.path.basename(src)))

        accounting = {
            "dict_bytes": len(dict_payload),
            "remap_bytes": state["remap_bytes"],
            "spill_bytes": sum(
                c["spill_bytes"] for c in state["collectors"].values()
            ),
            "output_bytes": state["output_bytes"],
        }
        with open(os.path.join(vdir, "ACCOUNTING.json"), "w") as f:
            json.dump(accounting, f, indent=2)
        _fsync_dir(vdir)

        if self.fault_before_commit is not None:
            self.fault_before_commit()  # simulated crash: old version intact

        self.store.commit_version(version)
        return MergeResult(
            version=version,
            batches=len(batches),
            nrows=sum(b.nrows for b in batches),
            accounting=accounting,
        )
