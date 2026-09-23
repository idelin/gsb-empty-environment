"""Test consumer: dedups redelivered output batches by their stable ID."""
from __future__ import annotations

from collections import Counter


class TestConsumer:
    def __init__(self):
        self.batches = {}     # output_id -> rows (first delivery wins)
        self.duplicates = 0   # redeliveries seen after recovery

    def record(self, output_batch):
        if output_batch.batch_id in self.batches:
            self.duplicates += 1
            return False
        self.batches[output_batch.batch_id] = list(output_batch.rows)
        return True

    def ack_all(self, engine):
        for output_id in list(self.batches):
            engine.ack(output_id)

    def multiset(self):
        result = Counter()
        for rows in self.batches.values():
            result.update(rows)
        return result

    def row_count(self):
        return sum(len(rows) for rows in self.batches.values())
