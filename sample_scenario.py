from __future__ import annotations

from piecewise1d import (
    Measurement,
    Node,
    PiecewiseModel,
)

SEED_STEPS = (2, 3, 5, 7, 11, 13)


def seeded_noise(step: int, channel: int) -> float:
    value = (SEED_STEPS[step % len(SEED_STEPS)] * (channel + 3) + step * 17) % 23
    return value / 100.0


def fmt_interval(value) -> str:
    evidence = "-" if value.evidence_id is None else f"E{value.evidence_id}"
    sources = ",".join(source.node_id for source in value.sources)
    return f"[{value.lower:.4f},{value.upper:.4f}] src={sources} evidence={evidence}"


def print_change(title: str, report) -> None:
    print(
        f"{title}: revision={report.revision} channels={report.rebuilt_channels} "
        f"rebuilt={report.rebuilt_segments} revoked={report.revoked_evidence} "
        f"created={report.created_evidence}"
    )


def main() -> None:
    model = PiecewiseModel(channel_count=3)
    nodes = {}

    for channel in range(3):
        for index, x in enumerate((0.0, 2.0, 4.0)):
            node_id = f"c{channel}-x{index}"
            center = 1.0 + channel + 0.25 * index
            if channel == 2:
                center = (2.0, 0.0, 2.0)[index]
            radius = 0.05 + 0.01 * index
            node = Node(
                node_id=node_id,
                channel=channel,
                x=x,
                measurement=Measurement(center - radius, center + radius),
                priority=index,
                version=1 + index,
                hard_break=(channel == 1 and index == 1),
            )
            nodes[node_id] = node
            print_change(f"add {node_id}", model.add_node(node))

    initial = model.snapshot

    print("\nValues:")
    for channel, x in ((0, 1.0), (1, 1.0), (1, 2.0), (1, 3.0), (2, 3.5)):
        try:
            print(f"channel={channel} x={x:.1f} {fmt_interval(model.value_at(channel, x))}")
        except Exception as exc:
            print(f"channel={channel} x={x:.1f} unavailable: {type(exc).__name__}: {exc}")

    print("\nDerivatives:")
    for channel, x in ((0, 1.0), (0, 2.0), (1, 2.0)):
        try:
            result = model.derivative_at(channel, x)
            print(f"channel={channel} x={x:.1f} interval=[{result.lower:.4f},{result.upper:.4f}] "
                  f"left={result.left} right={result.right}")
        except Exception as exc:
            print(f"channel={channel} x={x:.1f} unavailable: {type(exc).__name__}: {exc}")

    print("\nDeterministic inverse queries:")
    for channel, target in ((0, 1.3), (2, 1.0), (2, 2.0)):
        solutions = model.inverse_at(channel, target, include_uncertain=False)
        rendered = [(round(item.lower, 4), round(item.upper, 4), item.kind) for item in solutions]
        print(f"channel={channel} target={target:.1f} solutions={rendered}")

    old_node = nodes["c0-x1"]
    old_evidence = model.value_at(0, 1.0).evidence_id
    print("\nCalibration snapshot check:")
    print(f"before revision={initial.revision} value={model.value_at(0, 1.0, initial).nominal:.4f}")
    radius = old_node.measurement.upper - old_node.measurement.nominal
    report = model.calibrate(
        "c0-x1",
        measurement=Measurement(2.25 - radius, 2.25 + radius),
        version=10,
    )
    print_change("calibrate c0-x1", report)
    print(f"after current value={model.value_at(0, 1.0).nominal:.4f}")
    print(f"old snapshot remains={model.value_at(0, 1.0, initial).nominal:.4f}")
    print(f"old evidence E{old_evidence} revoked={model.evidence(old_evidence).revoked}")

    print("\nInsert and delete/restore:")
    transient = Node(
        node_id="c0-insert",
        channel=0,
        x=3.0,
        measurement=Measurement(3.0, 3.2),
        priority=4,
        version=1,
        hard_break=False,
    )
    print_change("insert c0-insert", model.add_node(transient))
    insert_result = model.value_at(0, 2.5)
    print(f"after insert x=2.5 {fmt_interval(insert_result)}")
    print_change("delete c0-insert", model.delete_node("c0-insert"))
    print_change("restore c0-insert", model.restore_node("c0-insert"))
    restored_result = model.value_at(0, 2.5)
    print(f"after restore x=2.5 {fmt_interval(restored_result)}")

    print("\nLocal rebuild isolation:")
    report = model.calibrate("c2-x1", measurement=Measurement(2.0, 2.2), version=99)
    print_change("calibrate c2-x1", report)

    print("\nReproducible seeded evidence samples:")
    for step, channel in enumerate(SEED_STEPS):
        x = 0.5 + 0.6 * (step % 5)
        try:
            result = model.value_at(channel % model.channel_count, x)
            print(f"step={step} noise={seeded_noise(step, channel):.2f} "
                  f"channel={channel % model.channel_count} x={x:.1f} {fmt_interval(result)}")
        except Exception as exc:
            print(f"step={step} unavailable: {type(exc).__name__}")


if __name__ == "__main__":
    main()
