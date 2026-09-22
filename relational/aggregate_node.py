from __future__ import annotations

from dataclasses import dataclass

from .aggregates import DISTRIBUTIVE_FUNCTIONS, empty_state, finalize_state, merge_states, update_state
from .operators import Node
from .relations import Column, Provenance, Relation, Row, Schema


@dataclass(frozen=True)
class Aggregate(Node):
    source: Node
    group_keys: tuple[Column, ...]
    aggregates: tuple
    mode: str = "complete"
    outer_pad: bool = False

    def __post_init__(self) -> None:
        if self.mode not in {"complete", "partial", "final"}:
            raise ValueError("aggregate mode must be complete, partial, or final")
        for aggregate in self.aggregates:
            if self.mode == "complete" and aggregate.function not in DISTRIBUTIVE_FUNCTIONS | {"median"}:
                raise ValueError(f"unsupported aggregate {aggregate.function}")
            if self.mode != "complete" and aggregate.function not in DISTRIBUTIVE_FUNCTIONS:
                raise ValueError(f"aggregate {aggregate.function} is not distributive")

    @property
    def schema(self) -> Schema:
        if self.mode == "partial":
            return Schema(tuple(self.group_keys) + tuple(item.state_column for item in self.aggregates))
        return Schema(tuple(self.group_keys) + tuple(item.output for item in self.aggregates))

    def execute(self, tables: dict) -> Relation:
        rows = self.source.execute(tables).rows
        if self.mode == "partial":
            return self._execute_partial(rows)
        if self.mode == "final":
            return self._execute_final(rows)
        return self._execute_complete(rows)

    def _grouped(self, rows: tuple[Row, ...]) -> dict[tuple, list[Row]]:
        groups: dict[tuple, list[Row]] = {}
        for row in rows:
            key = tuple(row[column] for column in self.group_keys)
            groups.setdefault(key, []).append(row)
        return groups

    def _execute_complete(self, rows: tuple[Row, ...]) -> Relation:
        groups = self._grouped(rows)
        if not self.group_keys and not rows:
            groups[()] = []
        output = []
        for key, grouped_rows in groups.items():
            values = list(key)
            for aggregate in self.aggregates:
                if aggregate.function == "median":
                    source_values = sorted(
                        value
                        for row in grouped_rows
                        if aggregate.source is not None and (value := aggregate.source.evaluate(row)) is not None
                    )
                    if not source_values:
                        result = None
                    else:
                        middle = len(source_values) // 2
                        result = (
                            source_values[middle]
                            if len(source_values) % 2
                            else (source_values[middle - 1] + source_values[middle]) / 2
                        )
                else:
                    state = empty_state(aggregate.function)
                    for row in grouped_rows:
                        state = update_state(aggregate.function, state, row, aggregate.source)
                    result = finalize_state(aggregate.function, state)
                values.append(result)
            provenance = grouped_rows[0].provenance if grouped_rows else Provenance(-1, -1)
            output.append(Row(self.schema, tuple(values), provenance))
        return Relation(self.schema, tuple(output))

    def _execute_partial(self, rows: tuple[Row, ...]) -> Relation:
        groups = self._grouped(rows)
        output = []
        for key, grouped_rows in groups.items():
            values = list(key)
            for aggregate in self.aggregates:
                state = empty_state(aggregate.function)
                for row in grouped_rows:
                    state = update_state(aggregate.function, state, row, aggregate.source)
                values.append(state)
            output.append(Row(self.schema, tuple(values), grouped_rows[0].provenance))
        return Relation(self.schema, tuple(output))

    def _execute_final(self, rows: tuple[Row, ...]) -> Relation:
        groups = self._grouped(rows)
        if not self.group_keys and not rows:
            groups[()] = []
        output = []
        for key, grouped_rows in groups.items():
            values = list(key)
            for aggregate in self.aggregates:
                state = empty_state(aggregate.function)
                matched_rows = 0
                outer_rows = 0
                for row in grouped_rows:
                    incoming = row[aggregate.state_column]
                    if incoming is None:
                        outer_rows += 1
                        continue
                    matched_rows += 1
                    state = merge_states(aggregate.function, state, incoming)
                if self.outer_pad and matched_rows == 0 and outer_rows:
                    if aggregate.function == "count_star":
                        values.append(1)
                    elif aggregate.function == "count":
                        values.append(0)
                    else:
                        values.append(None)
                else:
                    values.append(finalize_state(aggregate.function, state))
            provenance = grouped_rows[0].provenance if grouped_rows else Provenance(-1, -1)
            output.append(Row(self.schema, tuple(values), provenance))
        return Relation(self.schema, tuple(output))
