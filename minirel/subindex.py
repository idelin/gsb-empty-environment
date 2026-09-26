"""Decorrelated subquery index.

For one Subquery we precompute, over all inner rows that pass the inner
filter:

    corr_key -> { null_pattern -> { non_null_value_tuple: count } }

* corr_key is the tuple of inner correlation values.  A null component in a
* regular (non null-safe) condition can never correlate, so such rows are
* dropped at build time; null-safe components keep None as a key value.
* null_pattern records which select columns are null; the stored value tuple
  holds the non-null components in column order.

IN / NOT IN probing per outer row is then O(2^k) dict lookups per partition
(k = number of select columns) instead of a full inner scan.  Crucially the
null pattern is part of the key structure, so a null anywhere in the inner
table does NOT force every outer row to UNKNOWN: a row comparison is FALSE
as soon as one component is definitely unequal.

When the number of buffered rows exceeds the memory budget the in-memory map
is flushed to a spill partition; each partition file keeps the complete
null-pattern information for its rows, and per-partition results are combined
with three-valued OR, which is exactly OR over all inner rows.
"""

from collections import OrderedDict

from .errors import Cancelled
from .query_ast import eval_expr
from .tvl import tvl_or


class SubqueryIndex:
    def __init__(self, subquery, inner_rows, spill, mem_budget, stats,
                 cancel_check=None, partition_cache=2):
        self.n_select = len(subquery.select)
        self._spill = spill
        self._stats = stats
        self._partition_cache = max(1, partition_cache)
        self._cache = OrderedDict()
        self.mem = {}
        self.partitions = []
        self._rows_since_flush = 0
        self._build(subquery, inner_rows, mem_budget, cancel_check)

    def _build(self, sub, rows, mem_budget, cancel_check):
        for row in rows:
            if cancel_check is not None and cancel_check():
                raise Cancelled("cancelled while building subquery index")
            self._stats.inner_rows_indexed += 1
            if sub.filter is not None and eval_expr(sub.filter, row) is not True:
                continue
            key = []
            usable = True
            for cond in sub.corr:
                val = eval_expr(cond.inner, row)
                if val is None and not cond.null_safe:
                    usable = False
                    break
                key.append(val)
            if not usable:
                continue
            vals = tuple(eval_expr(e, row) for e in sub.select)
            pattern = tuple(i for i, v in enumerate(vals) if v is None)
            stored = tuple(v for v in vals if v is not None)
            group = self.mem.setdefault(tuple(key), {})
            bucket = group.setdefault(pattern, {})
            bucket[stored] = bucket.get(stored, 0) + 1
            self._rows_since_flush += 1
            if mem_budget is not None and self._rows_since_flush >= mem_budget:
                self._flush()

    def _flush(self):
        if self.mem:
            path = self._spill.spill(self.mem)
            self.partitions.append(path)
            self._stats.partitions_spilled += 1
            self.mem = {}
        self._rows_since_flush = 0

    def _groups(self):
        """Yield each partition's corr_key -> pattern map, memory first."""
        yield self.mem
        for path in self.partitions:
            cached = self._cache.get(path)
            if cached is not None:
                self._cache.move_to_end(path)
                yield cached
                continue
            group = self._spill.load(path)
            self._stats.partition_loads += 1
            self._cache[path] = group
            while len(self._cache) > self._partition_cache:
                self._cache.popitem(last=False)
            yield group

    def probe_exists(self, corr_key):
        for group in self._groups():
            self._stats.index_lookups += 1
            if corr_key in group:
                return True
        return False

    def probe_in(self, corr_key, outer_vals):
        """Three-valued IN over the correlated candidate set."""
        result = False
        for group in self._groups():
            self._stats.index_lookups += 1
            result = tvl_or(result, self._group_in(group.get(corr_key), outer_vals))
            if result is True:
                break
        return result

    def _group_in(self, patterns, outer_vals):
        """IN over one corr-key group: {null_pattern: {values: count}}."""
        if not patterns:
            return False
        k = len(outer_vals)
        outer_all_nonnull = all(v is not None for v in outer_vals)
        best = False
        for pattern, bucket in patterns.items():
            nonnull_cols = [i for i in range(k) if i not in pattern]
            if outer_all_nonnull:
                # Exact lookup: a row matches "no false component" iff its
                # non-null components equal the outer values.
                hit = tuple(outer_vals[i] for i in nonnull_cols) in bucket
                if hit and not pattern:
                    return True
            else:
                # Outer nulls make components unknown; a hit can at best be
                # UNKNOWN.  A row matches iff every non-null inner component
                # is either compared against an outer null or equal.
                hit = any(
                    all(outer_vals[i] is None or outer_vals[i] == stored[j]
                        for j, i in enumerate(nonnull_cols))
                    for stored in bucket
                )
            if hit:
                best = None  # UNKNOWN
        return best
