"""Risk threshold hysteresis alert machine.

Processes a sequenced numeric stream. High threshold H triggered m
consecutive times raises an alert; low threshold L triggered n consecutive
times resolves it (L < H). Values strictly between L and H, and missing
values, reset both consecutive counters without counting as high or low.

Duplicate sequence numbers with the same value are idempotent; a different
value for an already-seen sequence number is rejected. Configuration changes
apply only to subsequent records and reset the counters. State is persisted
atomically so a restart never re-emits a state-change event.

Python 3.11 standard library only.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile
from dataclasses import dataclass, asdict

STATE_NORMAL = "NORMAL"
STATE_ALERT = "ALERT"

EVENT_ALERT = "ALERT"
EVENT_RESOLVE = "RESOLVE"

MISSING_TOKENS = {"MISSING", "NULL", "NAN", ""}


@dataclass(frozen=True)
class Config:
    high: float  # H: value >= high counts as a high trigger
    low: float   # L: value <= low counts as a low trigger
    m: int       # consecutive high triggers required to alert
    n: int       # consecutive low triggers required to resolve

    def __post_init__(self) -> None:
        if not self.low < self.high:
            raise ValueError(f"require L < H, got L={self.low}, H={self.high}")
        if self.m < 1 or self.n < 1:
            raise ValueError(f"require m >= 1 and n >= 1, got m={self.m}, n={self.n}")


class AlertMachine:
    """Pure hysteresis state machine (no sequencing or persistence)."""

    def __init__(self, config: Config, state: str = STATE_NORMAL,
                 high_count: int = 0, low_count: int = 0) -> None:
        if state not in (STATE_NORMAL, STATE_ALERT):
            raise ValueError(f"unknown state: {state!r}")
        self.config = config
        self.state = state
        self.high_count = high_count
        self.low_count = low_count

    def reconfigure(self, config: Config) -> None:
        """Switch config; affects only subsequent records, resets counters."""
        self.config = config
        self.high_count = 0
        self.low_count = 0

    def process(self, value: float | None) -> str | None:
        """Consume one value (None = missing). Return an event name or None."""
        if value is None:
            self.high_count = 0
            self.low_count = 0
            return None
        if value >= self.config.high:
            self.high_count += 1
            self.low_count = 0
        elif value <= self.config.low:
            self.low_count += 1
            self.high_count = 0
        else:
            self.high_count = 0
            self.low_count = 0

        if self.state == STATE_NORMAL and self.high_count >= self.config.m:
            self.state = STATE_ALERT
            self.high_count = 0
            self.low_count = 0
            return EVENT_ALERT
        if self.state == STATE_ALERT and self.low_count >= self.config.n:
            self.state = STATE_NORMAL
            self.high_count = 0
            self.low_count = 0
            return EVENT_RESOLVE
        return None


def parse_value(raw) -> float | None:
    """Parse a JSON-decoded value; None / missing tokens mean 'missing'."""
    if raw is None:
        return None
    if isinstance(raw, str):
        if raw.strip().upper() in MISSING_TOKENS:
            return None
        return float(raw)
    if isinstance(raw, bool):
        raise ValueError(f"invalid value: {raw!r}")
    return float(raw)


class StateStore:
    """Atomic JSON-file persistence for the machine snapshot."""

    def __init__(self, path: str) -> None:
        self.path = path

    def load(self) -> dict | None:
        if not os.path.exists(self.path):
            return None
        with open(self.path, "r", encoding="utf-8") as fh:
            return json.load(fh)

    def save(self, snapshot: dict) -> None:
        directory = os.path.dirname(os.path.abspath(self.path))
        fd, tmp = tempfile.mkstemp(dir=directory, prefix=".state-", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                json.dump(snapshot, fh, ensure_ascii=False, sort_keys=True)
                fh.write("\n")
            os.replace(tmp, self.path)
        except BaseException:
            try:
                os.unlink(tmp)
            except OSError:
                pass
            raise


class SequencedAlertProcessor:
    """AlertMachine + sequence-number dedup + persistence."""

    def __init__(self, machine: AlertMachine, store: StateStore | None = None,
                 last_seq: int | None = None, last_value=None) -> None:
        self.machine = machine
        self.store = store
        self.last_seq = last_seq
        self.last_value = last_value  # raw JSON value of last accepted record

    @classmethod
    def restore(cls, config: Config, store: StateStore) -> "SequencedAlertProcessor":
        snap = store.load()
        if snap is None:
            return cls(AlertMachine(config), store)
        machine = AlertMachine(
            config=config,
            state=snap["state"],
            high_count=snap["high_count"],
            low_count=snap["low_count"],
        )
        stored_config = Config(**snap["config"])
        if stored_config != config:
            machine.reconfigure(config)
        return cls(machine, store, snap.get("last_seq"), snap.get("last_value"))

    def _snapshot(self) -> dict:
        return {
            "config": asdict(self.machine.config),
            "state": self.machine.state,
            "high_count": self.machine.high_count,
            "low_count": self.machine.low_count,
            "last_seq": self.last_seq,
            "last_value": self.last_value,
        }

    def _persist(self) -> None:
        if self.store is not None:
            self.store.save(self._snapshot())

    def process(self, seq: int, raw_value) -> dict:
        """Process one record; returns a result dict (never raises for bad seq)."""
        if not isinstance(seq, int) or isinstance(seq, bool) or seq < 0:
            return {"seq": seq, "status": "rejected",
                    "error": f"invalid seq: {seq!r}"}
        try:
            value = parse_value(raw_value)
        except (TypeError, ValueError):
            return {"seq": seq, "status": "rejected",
                    "error": f"invalid value: {raw_value!r}"}

        if self.last_seq is not None and seq <= self.last_seq:
            if seq == self.last_seq and raw_value == self.last_value:
                return {"seq": seq, "status": "duplicate",
                        "state": self.machine.state, "event": None}
            return {"seq": seq, "status": "rejected",
                    "error": (f"seq {seq} already processed"
                              + (" with a different value" if seq == self.last_seq
                                 else " (out of order)"))}

        event = self.machine.process(value)
        self.last_seq = seq
        self.last_value = raw_value
        self._persist()
        return {"seq": seq, "status": "processed",
                "state": self.machine.state, "event": event}


def load_config(path: str) -> Config:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    return Config(high=float(data["high"]), low=float(data["low"]),
                  m=int(data["m"]), n=int(data["n"]))


def run(config_path: str, state_path: str, lines) -> int:
    config = load_config(config_path)
    processor = SequencedAlertProcessor.restore(config, StateStore(state_path))
    exit_code = 0
    for line in lines:
        line = line.strip()
        if not line:
            continue
        try:
            record = json.loads(line)
            seq = record["seq"]
            raw_value = record.get("value")
        except (json.JSONDecodeError, AttributeError, KeyError) as exc:
            result = {"seq": None, "status": "rejected",
                      "error": f"malformed input line: {exc}"}
            exit_code = 1
        else:
            result = processor.process(seq, raw_value)
            if result["status"] == "rejected":
                exit_code = 1
        print(json.dumps(result, ensure_ascii=False), flush=True)
    return exit_code


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Risk threshold hysteresis alert machine (JSONL in/out).")
    parser.add_argument("--config", required=True,
                        help="JSON config file: {\"high\": H, \"low\": L, \"m\": m, \"n\": n}")
    parser.add_argument("--state", required=True,
                        help="JSON state file for persistence (created if absent)")
    parser.add_argument("--input", default=None,
                        help="input JSONL file (default: stdin)")
    args = parser.parse_args(argv)

    if args.input is None:
        return run(args.config, args.state, sys.stdin)
    with open(args.input, "r", encoding="utf-8") as fh:
        return run(args.config, args.state, fh)


if __name__ == "__main__":
    sys.exit(main())
