"""Plan explanation for the decorrelated (optimized) executor."""

from .query_ast import (
    And, Col, Eq, ExistsPred, InPred, Lit, Not, Or, find_subquery_preds,
)


def render(e):
    if isinstance(e, Col):
        return f"c{e.idx}"
    if isinstance(e, Lit):
        return "NULL" if e.val is None else repr(e.val)
    if isinstance(e, Eq):
        op = "<=>" if e.null_safe else "="
        return f"({render(e.left)} {op} {render(e.right)})"
    if isinstance(e, And):
        return "(" + " AND ".join(render(a) for a in e.args) + ")"
    if isinstance(e, Or):
        return "(" + " OR ".join(render(a) for a in e.args) + ")"
    if isinstance(e, Not):
        return f"(NOT {render(e.arg)})"
    if isinstance(e, InPred):
        op = "NOT IN" if e.negated else "IN"
        cols = ", ".join(render(a) for a in e.outer)
        return f"({cols}) {op} <subquery:{e.sub.table}>"
    if isinstance(e, ExistsPred):
        op = "NOT EXISTS" if e.negated else "EXISTS"
        return f"{op} <subquery:{e.sub.table}>"
    return repr(e)


def explain(query, mode="optimized"):
    lines = [f"Plan ({mode}):"]
    projections = ", ".join(render(e) for e in query.select)
    lines.append(f"  Project[{projections}]")
    if query.where is not None:
        lines.append(f"    Where[{render(query.where)}]  (keep TRUE only)")
    if mode == "interpreter":
        lines.append("    Scan[outer] x full inner scan per outer row")
        return "\n".join(lines)
    for pred in find_subquery_preds(query):
        sub = pred.sub
        if isinstance(pred, ExistsPred):
            op = "AntiJoin[NOT EXISTS]" if pred.negated else "SemiJoin[EXISTS]"
        else:
            op = "MarkerJoin[NOT IN]" if pred.negated else "MarkerJoin[IN]"
        corr = ", ".join(
            f"{render(c.outer)} {'<=>' if c.null_safe else '='} "
            f"{sub.table}.{render(c.inner)}"
            for c in sub.corr
        ) or "(uncorrelated)"
        select = ", ".join(render(e) for e in sub.select) or "(none)"
        filt = render(sub.filter) if sub.filter is not None else "(none)"
        lines.append(
            f"    {op} on {sub.table}: corr=[{corr}] "
            f"select=[{select}] filter=[{filt}]"
        )
    lines.append("    index: corr_key -> null_pattern -> values -> count"
                 " (spills to partitions over memory budget)")
    return "\n".join(lines)
