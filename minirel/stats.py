"""Execution counters."""

from dataclasses import dataclass


@dataclass
class Stats:
    outer_rows: int = 0
    result_rows: int = 0
    # interpreter: inner rows examined (outer x inner for uncorrelated scans)
    inner_rows_scanned: int = 0
    # optimized: inner rows fed into the index (each exactly once)
    inner_rows_indexed: int = 0
    # optimized: hash probes against the index / spill partitions
    index_lookups: int = 0
    partitions_spilled: int = 0
    partition_loads: int = 0
