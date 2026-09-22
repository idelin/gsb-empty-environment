"""Executor: counts consumers per subplan, decides materialize vs recompute,
and runs plan DAGs while sharing materialized results.

Sharing is keyed on the *structural* identity of plan nodes, so only
identical subplans are shared.  Two consumers with different filters share
the subplan below their filters; each filter is applied per consumer on
top of the shared (unfiltered) result.
"""
from __future__ import annotations

from collections import Counter

import relalg
from decision import CostModel, decide
from materialize import Catalog
from plan import PREDICATES, Filter, Join, Project, Scan, schema_of


def estimate_rows(node, tables, memo):
    key = node.key()
    if key in memo:
        return memo[key]
    if isinstance(node, Scan):
        est = len(tables[node.table_name].rows)
    elif isinstance(node, Filter):
        est = max(1, int(estimate_rows(node.child, tables, memo) * node.selectivity))
    elif isinstance(node, Project):
        est = estimate_rows(node.child, tables, memo)
    elif isinstance(node, Join):
        est = max(estimate_rows(node.left, tables, memo),
                  estimate_rows(node.right, tables, memo))
    else:
        raise TypeError(node)
    memo[key] = est
    return est


def compute_cost(node, tables, model, est_memo, memo):
    """Estimated cost of producing this subtree once (bottom-up)."""
    key = node.key()
    if key in memo:
        return memo[key]
    cost = estimate_rows(node, tables, est_memo) * model.cpu_per_row
    for child in node.children():
        cost += compute_cost(child, tables, model, est_memo, memo)
    memo[key] = cost
    return cost


class Executor:
    def __init__(self, tables, catalog=None, model=None):
        self.tables = {t.name: t for t in tables}
        self.catalog = catalog or Catalog()
        self.model = model or CostModel()
        self.decisions = {}      # node key -> Decision
        self.decision_log = []   # (node, Decision) in analysis order

    def _index(self, roots):
        counts = Counter()
        nodes = {}

        def walk(node):
            counts[node.key()] += 1
            if node.key() not in nodes:
                nodes[node.key()] = node
            for child in node.children():
                walk(child)

        for root in roots:
            walk(root)
        return counts, nodes

    def _analyze(self, roots):
        counts, nodes = self._index(roots)
        est_memo, cost_memo = {}, {}
        self.decisions = {}
        self.decision_log = []
        for key, node in nodes.items():
            if isinstance(node, Scan):
                continue  # base tables are always available; never materialized
            est = estimate_rows(node, self.tables, est_memo)
            cost = compute_cost(node, self.tables, self.model, est_memo, cost_memo)
            d = decide(counts[key], est, cost, self.model)
            self.decisions[key] = d
            self.decision_log.append((node, d))

    def run_all(self, roots):
        """Run several query roots together, sharing common subplans."""
        self._analyze(roots)
        return [list(self._execute(root)) for root in roots]

    def run(self, root):
        return self.run_all([root])[0]

    def _execute(self, node):
        key = node.key()
        d = self.decisions.get(key)
        if d is not None and d.materialized:
            view = self.catalog.get(key)
            if view is None:  # first consumer builds it; others reuse it
                spill = d.strategy.endswith("spill")
                self.catalog.publish(key, self._eval(node), spill=spill)
                view = self.catalog.get(key)
            return iter(view.read())
        return self._eval(node)

    def _eval(self, node):
        if isinstance(node, Scan):
            return self.tables[node.table_name].scan()
        if isinstance(node, Filter):
            pred = PREDICATES[node.pred_name]
            schema = schema_of(node.child, self.tables)
            rows = self._execute(node.child)
            return (row for row in rows
                    if pred(dict(zip(schema, row))) is True)
        if isinstance(node, Project):
            schema = schema_of(node.child, self.tables)
            idx = [schema.index(c) for c in node.cols]
            return relalg.project_rows(self._execute(node.child), idx)
        if isinstance(node, Join):
            lschema = schema_of(node.left, self.tables)
            rschema = schema_of(node.right, self.tables)
            lidx = [lschema.index(c) for c in node.left_keys]
            ridx = [rschema.index(c) for c in node.right_keys]
            return relalg.hash_join_rows(self._execute(node.left),
                                         self._execute(node.right),
                                         lidx, ridx)
        raise TypeError(node)
