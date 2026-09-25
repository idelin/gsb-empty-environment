"""Deterministic replayer for scenario scripts."""
from __future__ import annotations

import json
import os
from dataclasses import dataclass
from typing import Optional

from .network import Envelope, Network
from .node import Node
from .persistence import CrashPoint, InjectedCrash
from .scenarios import Op, Scenario
from .tag import Tag
from .time_model import Duration
from .trace import TraceCollector
from .validator import CausalValidator, ValidationReport


@dataclass
class Artifact:
    scenario: str
    trace: list[dict]
    validation: dict

    def to_json(self) -> str:
        return json.dumps(
            {
                "scenario": self.scenario,
                "trace": self.trace,
                "validation": self.validation,
            },
            indent=2,
            sort_keys=True,
        )


class Replayer:
    def __init__(self, scenario: Scenario, datadir: str) -> None:
        self.scenario = scenario
        self.datadir = datadir
        os.makedirs(datadir, exist_ok=True)
        self.network = Network()
        self.trace = TraceCollector()
        self.nodes: dict[str, Node] = {}

    def run(self) -> Artifact:
        skew = Duration.micros_of(self.scenario.allowed_skew_micros)
        for node_id, start in self.scenario.starts.items():
            self.nodes[node_id] = Node(
                node_id=node_id,
                datadir=self.datadir,
                network=self.network,
                trace=self.trace,
                start=start,
                table=self.scenario.tables[node_id],
                max_logical=self.scenario.max_logical,
                allowed_skew=skew,
                fresh=True,
            )
        for op in self.scenario.ops:
            self._apply(op)
        report = CausalValidator(max_logical=self.scenario.max_logical).validate(
            self.trace.events
        )
        return Artifact(
            scenario=self.scenario.name,
            trace=self.trace.to_list(),
            validation=report.to_dict(),
        )

    def _apply(self, op: Op) -> None:
        node = self.nodes.get(op.node)
        if op.action == "advance":
            assert node is not None
            node.advance_raw(op.raw_delta)
            return
        if op.action == "install_table":
            assert node is not None and op.table is not None
            node.install_table(op.table)
            return
        if op.action == "restart":
            assert op.start is not None and op.table is not None
            self.nodes[op.node] = Node.restart(
                node_id=op.node,
                datadir=self.datadir,
                network=self.network,
                trace=self.trace,
                start=op.start,
                table=op.table,
                max_logical=self.scenario.max_logical,
                allowed_skew=Duration.micros_of(self.scenario.allowed_skew_micros),
            )
            return
        if op.action == "duplicate":
            self.network.redeliver(op.node)
            return
        if op.action == "inject_future":
            assert node is not None
            wall = node.wall_now().micros
            tag = Tag(
                physical=wall + op.future_micros,
                logical=op.remote_logical,
                epoch=op.remote_epoch,
                node="attacker",
            )
            self.network.inject(
                Envelope(
                    msg_id=op.msg_id or "evil",
                    source="attacker",
                    target=op.node,
                    tag=tag,
                    payload=op.payload,
                )
            )
            return

        assert node is not None
        if op.action == "local":
            self._crash_guarded(lambda: node.local_event(op.crash))
        elif op.action == "send":
            assert op.target is not None and op.msg_id is not None
            self._crash_guarded(
                lambda: node.send(
                    op.msg_id, op.target, payload=op.payload, crash=op.crash
                )
            )
        elif op.action == "deliver":
            node.deliver()
        else:
            raise ValueError(f"unknown op action: {op.action}")

    def _crash_guarded(self, fn) -> None:
        try:
            fn()
        except InjectedCrash:
            return
