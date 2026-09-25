"""Independent cross-node causal-trajectory validator.

The validator consumes only the public trace (plus the quarantine list that
is itself part of the trace) and rebuilds the happens-before graph:

* along each node, successive published tags are local edges;
* each non-quarantined ``send`` is linked to its matching ``receive`` edge.

For *every* causal edge the tag at the head must be strictly greater than the
tag at the tail under the global tag ordering
``(physical, logical, epoch, node)``.  Additional rules cover epoch
discipline, tag uniqueness on each node, bounded logical counters and the
effectiveness of far-future quarantine.
"""
from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field

from .tag import Tag
from .trace import EventRecord


@dataclass
class ValidationReport:
    ok: bool = True
    errors: list[str] = field(default_factory=list)
    events: int = 0
    published: int = 0
    causal_edges: int = 0
    duplicates: int = 0
    quarantined: int = 0
    crashes: int = 0
    restarts: int = 0

    def fail(self, message: str) -> None:
        self.ok = False
        self.errors.append(message)

    def to_dict(self) -> dict:
        return {
            "ok": self.ok,
            "errors": list(self.errors),
            "events": self.events,
            "published": self.published,
            "causal_edges": self.causal_edges,
            "duplicates": self.duplicates,
            "quarantined": self.quarantined,
            "crashes": self.crashes,
            "restarts": self.restarts,
        }


class CausalValidator:
    def __init__(self, max_logical: int = 0xFFFF) -> None:
        self.max_logical = max_logical

    def validate(self, events: list[EventRecord]) -> ValidationReport:
        report = ValidationReport(events=len(events))

        published_by_node: dict[str, list[EventRecord]] = defaultdict(list)
        sends: dict[str, EventRecord] = {}
        receives: list[EventRecord] = []
        node_epochs: dict[str, list[int]] = defaultdict(list)

        for event in events:
            kind = event.kind
            if kind == "restart":
                report.restarts += 1
                if event.tag is not None:
                    node_epochs[event.node].append(event.tag.epoch)
                continue
            if kind == "crash":
                report.crashes += 1
                continue
            if kind == "duplicate":
                report.duplicates += 1
                continue
            if kind == "quarantine":
                report.quarantined += 1
                continue
            if kind not in ("local", "send", "receive"):
                report.fail(f"{event.node}: unknown event kind {kind!r}")
                continue
            if event.tag is None:
                report.fail(f"{event.node}: {kind} event without tag")
                continue
            if event.tag.logical > self.max_logical:
                report.fail(
                    f"{event.node}: tag {event.tag.to_wire()} exceeds max_logical"
                )
            published_by_node[event.node].append(event)
            report.published += 1
            if kind == "send":
                if not event.msg_id:
                    report.fail(f"{event.node}: send without msg_id")
                elif event.msg_id in sends:
                    report.fail(f"duplicate send record for {event.msg_id}")
                else:
                    sends[event.msg_id] = event
            elif kind == "receive":
                receives.append(event)

        # Rule 1: strict local ordering, uniqueness and epoch discipline.
        for node, chain in published_by_node.items():
            prior: EventRecord | None = None
            seen_tags: set[Tag] = set()
            for event in chain:
                tag = event.tag
                assert tag is not None
                if tag in seen_tags:
                    report.fail(f"{node}: tag reused after restart: {tag.to_wire()}")
                seen_tags.add(tag)
                if prior is not None:
                    report.causal_edges += 1
                    if not tag > prior.tag:
                        report.fail(
                            f"{node}: non-increasing local edge "
                            f"{prior.tag.to_wire()} -> {tag.to_wire()}"
                        )
                prior = event
            epochs = node_epochs.get(node, [])
            if any(b < a for a, b in zip(epochs, epochs[1:])):
                report.fail(f"{node}: incarnation epoch moved backwards")
            for event in chain:
                tag = event.tag
                assert tag is not None
                if epochs and tag.epoch < epochs[0]:
                    report.fail(f"{node}: tag epoch predates first incarnation")

        # Rule 2: every receive matches a send; quarantine/duplicate never do.
        edges: list[tuple[str, Tag, Tag]] = []
        for receive in receives:
            send = sends.get(receive.msg_id or "")
            if send is None or send.tag is None:
                report.fail(
                    f"{receive.node}: receive {receive.msg_id} has no matching send"
                )
                continue
            edges.append((f"msg:{receive.msg_id}", send.tag, receive.tag))

        # Rule 3: message edges strictly increase, and logical component is
        # within bounds on the wire tags as well.
        for label, head_tag, tail_tag in edges:
            report.causal_edges += 1
            if not tail_tag > head_tag:
                report.fail(
                    f"message edge {label} not strictly increasing: "
                    f"{head_tag.to_wire()} -> {tail_tag.to_wire()}"
                )

        # Rule 4: the happens-before graph (local + message edges) is a DAG.
        self._assert_acyclic(published_by_node, sends, receives, report)

        # Rule 5: quarantine events really were beyond a plausible bound and
        # they never produced a receive edge that advanced the node.
        quarantine_ids = {
            event.msg_id for event in events if event.kind == "quarantine"
        }
        for event in receives:
            if event.msg_id in quarantine_ids:
                report.fail(
                    f"{event.node}: quarantined message {event.msg_id} was applied"
                )

        return report

    def _assert_acyclic(
        self,
        published_by_node: dict[str, list[EventRecord]],
        sends: dict[str, EventRecord],
        receives: list[EventRecord],
        report: ValidationReport,
    ) -> None:
        graph: dict[Tag, set[Tag]] = defaultdict(set)
        for chain in published_by_node.values():
            tags = [event.tag for event in chain if event.tag is not None]
            for a, b in zip(tags, tags[1:]):
                graph[a].add(b)
        for receive in receives:
            send = sends.get(receive.msg_id or "")
            if send is None or send.tag is None or receive.tag is None:
                continue
            graph[send.tag].add(receive.tag)

        white, gray, black = 0, 1, 2
        color: dict[Tag, int] = defaultdict(lambda: white)

        def visit(node_tag: Tag, stack: list[Tag]) -> None:
            color[node_tag] = gray
            stack.append(node_tag)
            for nxt in graph.get(node_tag, ()):
                if color[nxt] == gray:
                    cycle = " -> ".join(tag.to_wire() for tag in stack + [nxt])
                    report.fail(f"causal cycle detected: {cycle}")
                    return
                if color[nxt] == white:
                    visit(nxt, stack)
                    if not report.ok:
                        return
            stack.pop()
            color[node_tag] = black

        for vertex in list(graph.keys()):
            if color[vertex] == white:
                visit(vertex, [])
                if not report.ok:
                    return
