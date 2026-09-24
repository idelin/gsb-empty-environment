"""Block-addressed external segment storage with access accounting.

Candidate overflow is spilled to immutable sorted segments. Each segment is
stored as one block in a file on disk; every read/write of a block is
counted so tests and demos can assert that backfill never degrades into a
full group scan.
"""
import os
import pickle


class BlockStore:
    def __init__(self, directory):
        self.directory = directory
        os.makedirs(directory, exist_ok=True)
        self.reads = 0
        self.writes = 0
        self.next_block = 0

    def _path(self, bid):
        return os.path.join(self.directory, "blk-%08d.seg" % bid)

    def write_segment(self, entries):
        """Persist a sorted list of keys; returns the block id."""
        bid = self.next_block
        self.next_block += 1
        with open(self._path(bid), "wb") as fh:
            pickle.dump(entries, fh)
        self.writes += 1
        return bid

    def read_segment(self, bid):
        with open(self._path(bid), "rb") as fh:
            entries = pickle.load(fh)
        self.reads += 1
        return entries

    def drop_segment(self, bid):
        try:
            os.remove(self._path(bid))
        except FileNotFoundError:
            pass

    def stats(self):
        return {"block_reads": self.reads, "block_writes": self.writes}
