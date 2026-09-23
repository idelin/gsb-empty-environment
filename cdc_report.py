from __future__ import annotations

from cdc_compare import ComparisonResult


def format_result(result: ComparisonResult) -> str:
    lines = [
        f"status={result.status}",
        f"left={result.left_name}",
        f"right={result.right_name}",
        f"common_watermark={result.common_watermark}",
    ]

    if result.lag is not None:
        lag = result.lag
        lines.append(
            "lag="
            f"{lag.side}:{lag.replica_name} "
            f"requested={lag.requested_watermark} "
            f"hwm={lag.replica_high_water_mark}"
        )
        return "\n".join(lines)

    lines.append(f"missing_events={len(result.missing_events)}")
    for item in result.missing_events:
        lines.append(
            "  - "
            f"{item.entity.table}:{item.entity.primary_key} "
            f"tx={item.event_key.transaction_id} seq={item.commit_seq} "
            f"missing_on={item.side} before={_value(item.before_value)} "
            f"after={_value(item.after_value)}"
        )

    lines.append(f"order_conflicts={len(result.order_conflicts)}")
    for item in result.order_conflicts:
        lines.append(
            "  - "
            f"{item.entity.table}:{item.entity.primary_key} "
            f"tx={item.event_key.transaction_id} "
            f"left_seq={item.left_commit_seq} right_seq={item.right_commit_seq}"
        )

    lines.append(
        f"event_value_divergences={len(result.event_value_divergences)}"
    )
    for item in result.event_value_divergences:
        lines.append(
            "  - "
            f"{item.entity.table}:{item.entity.primary_key} "
            f"tx={item.event_key.transaction_id} seq={item.commit_seq}"
        )
        lines.append(
            f"    before left={_value(item.left_before)} "
            f"right={_value(item.right_before)}"
        )
        lines.append(
            f"    after  left={_value(item.left_after)} "
            f"right={_value(item.right_after)}"
        )

    lines.append(
        f"final_value_divergences={len(result.final_value_divergences)}"
    )
    for item in result.final_value_divergences:
        lines.append(
            "  - "
            f"{item.entity.table}:{item.entity.primary_key} "
            f"left={_value(item.left_value)} right={_value(item.right_value)}"
        )

    return "\n".join(lines)


def _value(value: object) -> str:
    return repr(value)
