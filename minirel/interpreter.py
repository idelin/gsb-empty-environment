"""Independent row-at-a-time interpreter (reference semantics).

For every outer row the subquery is evaluated by scanning the inner table
and applying filter + correlation per inner row.  No decorrelation, no
indexing: this is the ground truth the optimized executor is checked
against.
"""

from .errors import Cancelled
from .query_ast import ExistsPred, eval_expr, eval_scalar
from .tvl import eq3, eq_nullsafe, row_eq, tvl_not, tvl_or


class Interpreter:
    def __init__(self, query, tables, stats, cancel_check=None):
        self.query = query
        self.tables = tables
        self.stats = stats
        self.cancel_check = cancel_check

    def _candidate(self, sub, inner_row, outer_row):
        """True iff the inner row passes filter AND all correlation conds."""
        if sub.filter is not None and eval_expr(sub.filter, inner_row) is not True:
            return False
        for cond in sub.corr:
            outer_val = eval_expr(cond.outer, outer_row)
            inner_val = eval_expr(cond.inner, inner_row)
            if cond.null_safe:
                matched = eq_nullsafe(outer_val, inner_val)
            else:
                matched = eq3(outer_val, inner_val)
            if matched is not True:
                return False
        return True

    def eval_pred(self, pred, outer_row):
        sub = pred.sub
        inner = self.tables[sub.table]
        if isinstance(pred, ExistsPred):
            result = False
            for inner_row in inner:
                self.stats.inner_rows_scanned += 1
                if self._candidate(sub, inner_row, outer_row):
                    result = True
                    break
        else:  # InPred
            outer_vals = tuple(eval_expr(e, outer_row) for e in pred.outer)
            result = False  # IN over an empty candidate set is FALSE
            for inner_row in inner:
                self.stats.inner_rows_scanned += 1
                if self._candidate(sub, inner_row, outer_row):
                    inner_vals = tuple(eval_expr(e, inner_row) for e in sub.select)
                    result = tvl_or(result, row_eq(outer_vals, inner_vals))
        return tvl_not(result) if pred.negated else result

    def run(self, outer_pages):
        out = []
        for page in outer_pages:
            for row in page:
                if self.cancel_check is not None and self.cancel_check():
                    raise Cancelled("cancelled during interpretation")
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
