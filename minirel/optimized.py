"""Optimized executor: decorrelates subqueries into joins.

EXISTS      -> semi-join on the correlation key
NOT EXISTS  -> anti-join on the correlation key
IN          -> marker join producing TRUE / FALSE / UNKNOWN per outer row
NOT IN      -> marker join + three-valued NOT

Every outer physical row is probed independently, so multiplicities of
duplicate outer rows are preserved.
"""

from .errors import Cancelled
from .query_ast import ExistsPred, eval_expr, eval_scalar, find_subquery_preds
from .subindex import SubqueryIndex
from .tvl import tvl_not


class OptimizedExecutor:
    def __init__(self, query, tables, stats, spill, mem_budget,
                 cancel_check=None, partition_cache=2):
        self.query = query
        self.stats = stats
        self.cancel_check = cancel_check
        self.indexes = {}
        for pred in find_subquery_preds(query):
            if pred.sub in self.indexes:
                continue
            self.indexes[pred.sub] = SubqueryIndex(
                pred.sub, tables[pred.sub.table], spill, mem_budget, stats,
                cancel_check=cancel_check, partition_cache=partition_cache,
            )

    def eval_pred(self, pred, outer_row):
        sub = pred.sub
        index = self.indexes[sub]
        key = []
        usable = True
        for cond in sub.corr:
            val = eval_expr(cond.outer, outer_row)
            if val is None and not cond.null_safe:
                usable = False  # regular equality with null never correlates
                break
            key.append(val)
        if not usable:
            result = False  # empty correlated candidate set
        elif isinstance(pred, ExistsPred):
            result = index.probe_exists(tuple(key))
        else:
            outer_vals = tuple(eval_expr(e, outer_row) for e in pred.outer)
            result = index.probe_in(tuple(key), outer_vals)
        return tvl_not(result) if pred.negated else result

    def run(self, outer_pages):
        out = []
        for page in outer_pages:
            for row in page:
                if self.cancel_check is not None and self.cancel_check():
                    raise Cancelled("cancelled during optimized execution")
                self.stats.outer_rows += 1
                keep = True
                if self.query.where is not None:
                    keep = eval_scalar(self.query.where, row, self.eval_pred)
                if keep is True:  # WHERE keeps only TRUE
                    projected = tuple(
                        eval_scalar(e, row, self.eval_pred)
                        for e in self.query.select
                    )
                    out.append(projected)
        self.stats.result_rows = len(out)
        return out
