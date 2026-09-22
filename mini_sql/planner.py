from .errors import BindError, UnsupportedSQL
from .parser import (
    BinaryExpr,
    ColumnRef,
    ExistsExpr,
    FunctionCall,
    Literal,
    ScalarSubquery,
    Star,
    UnaryExpr,
    parse_sql,
)
from .runtime import (
    BBinary,
    BColumn,
    BLiteral,
    BMarker,
    BUnary,
    Filter,
    GroupJoin,
    Project,
    Scan,
    SemiJoin,
)


AGGREGATES = {"COUNT", "SUM", "AVG", "MIN", "MAX"}


class Scope:
    def __init__(self, level, alias, columns):
        self.level = level
        self.alias = alias
        self.columns = tuple(columns)


def split_conjunction(expr):
    if isinstance(expr, BinaryExpr) and expr.op == "AND":
        return split_conjunction(expr.left) + split_conjunction(expr.right)
    return [expr]


def contains_subquery(expr):
    if isinstance(expr, (ExistsExpr, ScalarSubquery)):
        return True
    if isinstance(expr, UnaryExpr):
        return contains_subquery(expr.expr)
    if isinstance(expr, BinaryExpr):
        return contains_subquery(expr.left) or contains_subquery(expr.right)
    if isinstance(expr, FunctionCall):
        return any(contains_subquery(argument) for argument in expr.args)
    return False


class QueryPlanner:
    def __init__(self, database):
        self.database = database
        self.marker_counter = 0

    def plan(self, sql):
        query = parse_sql(sql)
        scope = self.make_scope(query.from_table, 0)
        plan = Scan(query.from_table.name, scope.alias, scope.columns, 0)
        if query.where is not None:
            plan = self.bind_outer_where(query.where, scope, plan)

        if query.has_star:
            star = query.items[0].expr
            star_columns = self.resolve_star(star, (scope,))
            schema = tuple(column for level, alias, column in star_columns)
            return Project(plan, star_columns=star_columns, schema=schema)

        outputs = []
        for item in query.items:
            if isinstance(item.expr, FunctionCall):
                raise UnsupportedSQL("outer aggregate queries are not supported; aggregates must be scalar subqueries")
            bound_expr, plan = self.bind_outer_expr(item.expr, (scope,), plan)
            output_name = item.alias or self.default_name(item.expr)
            outputs.append((output_name, bound_expr))
        schema = tuple(name for name, expr in outputs)
        return Project(plan, tuple(outputs), schema=schema)

    def execute(self, sql):
        return list(self.plan(sql).execute(self.database))

    def execute_with_schema(self, sql):
        plan = self.plan(sql)
        return plan.schema, list(plan.execute(self.database))

    def fresh_marker(self):
        self.marker_counter += 1
        return f"subquery_{self.marker_counter}"

    def make_scope(self, table_ref, level):
        table_name = table_ref.name
        relation = self.database.get(table_name)
        alias = table_ref.alias or table_name
        return Scope(level, alias, relation.columns)

    def bind_outer_where(self, expr, scope, plan):
        conjuncts = split_conjunction(expr)
        for conjunct in conjuncts:
            if isinstance(conjunct, ExistsExpr):
                plan = self.plan_exists(conjunct, (scope,), plan)
            elif contains_subquery(conjunct):
                raise UnsupportedSQL("scalar subqueries are supported only in SELECT items; EXISTS may be a WHERE conjunct")
            else:
                plan = Filter(plan, self.bind_expr(conjunct, (scope,)))
        return plan

    def bind_outer_expr(self, expr, outer_scopes, plan):
        if isinstance(expr, ScalarSubquery):
            marker, right, predicates, outer_predicates, aggregate, argument = self.plan_scalar(expr.query, outer_scopes)
            for predicate in outer_predicates:
                plan = Filter(plan, predicate)
            return BMarker(marker), GroupJoin(plan, right, predicates, marker, aggregate, argument)
        if isinstance(expr, ExistsExpr):
            raise UnsupportedSQL("EXISTS is supported only as a WHERE conjunct")
        if isinstance(expr, UnaryExpr):
            bound, plan = self.bind_outer_expr(expr.expr, outer_scopes, plan)
            return BUnary(expr.op, bound), plan
        if isinstance(expr, BinaryExpr):
            left, plan = self.bind_outer_expr_chain(expr.left, outer_scopes, plan)
            right, plan = self.bind_outer_expr_chain(expr.right, outer_scopes, plan)
            return BBinary(left, expr.op, right), plan
        return self.bind_expr(expr, outer_scopes), plan

    def bind_outer_expr_chain(self, expr, outer_scopes, plan):
        return self.bind_outer_expr(expr, outer_scopes, plan)

    def plan_exists(self, exists_expr, outer_scopes, left_plan):
        if not exists_expr.query.has_star:
            raise UnsupportedSQL("EXISTS subquery must select *")
        if exists_expr.query.items[0].expr.qualifier is not None:
            raise UnsupportedSQL("qualified '*' is not supported in EXISTS")
        right, predicates, outer_predicates = self.plan_inner_query(exists_expr.query, outer_scopes)
        for predicate in outer_predicates:
            left_plan = Filter(left_plan, predicate)
        return SemiJoin(left_plan, right, tuple(predicates), exists_expr.negated)

    def plan_scalar(self, query, outer_scopes):
        if len(query.items) != 1 or query.has_star:
            raise UnsupportedSQL("scalar subquery must project exactly one expression")
        item = query.items[0]
        aggregate_call = item.expr
        if not isinstance(aggregate_call, FunctionCall) or aggregate_call.name.upper() not in AGGREGATES:
            raise UnsupportedSQL("scalar subquery must project COUNT, SUM, AVG, MIN or MAX")

        inner_scope = self.make_scope(query.from_table, len(outer_scopes))
        scopes = outer_scopes + (inner_scope,)
        right, predicates, outer_predicates = self.plan_inner_relation(query, scopes)

        name = aggregate_call.name.upper()
        if aggregate_call.star:
            if name != "COUNT":
                raise UnsupportedSQL("only COUNT(*) is supported")
            argument = None
        else:
            if len(aggregate_call.args) != 1:
                raise UnsupportedSQL("aggregate in scalar subquery must have exactly one argument")
            argument = self.bind_expr(aggregate_call.args[0], scopes)
            if min_column_level(argument, default=inner_scope.level) < inner_scope.level:
                raise UnsupportedSQL("aggregate arguments cannot reference an outer query directly")

        marker = self.fresh_marker()
        return marker, right, tuple(predicates), outer_predicates, name, argument

    def plan_inner_query(self, query, outer_scopes):
        inner_scope = self.make_scope(query.from_table, len(outer_scopes))
        scopes = outer_scopes + (inner_scope,)
        return self.plan_inner_relation(query, scopes)

    def plan_inner_relation(self, query, scopes):
        inner_scope = scopes[-1]
        right = Scan(query.from_table.name, inner_scope.alias, inner_scope.columns, inner_scope.level)
        predicates = []
        outer_predicates = []
        if query.where is not None:
            for conjunct in split_conjunction(query.where):
                if contains_subquery(conjunct):
                    raise UnsupportedSQL("nested subqueries are not supported")
                bound = self.bind_expr(conjunct, scopes)
                levels = column_levels(bound)
                if not levels:
                    right = Filter(right, bound)
                elif max(levels) < inner_scope.level:
                    outer_predicates.append(bound)
                elif min(levels) < inner_scope.level:
                    predicates.append(bound)
                else:
                    right = Filter(right, bound)
        return right, predicates, outer_predicates

    def bind_expr(self, expr, scopes):
        if isinstance(expr, Literal):
            return BLiteral(expr.value)
        if isinstance(expr, ColumnRef):
            return self.bind_column(expr, scopes)
        if isinstance(expr, UnaryExpr):
            return BUnary(expr.op, self.bind_expr(expr.expr, scopes))
        if isinstance(expr, BinaryExpr):
            return BBinary(
                self.bind_expr(expr.left, scopes),
                expr.op,
                self.bind_expr(expr.right, scopes),
            )
        if isinstance(expr, FunctionCall):
            raise UnsupportedSQL("aggregate functions are supported only as the single scalar-subquery output")
        if isinstance(expr, (ExistsExpr, ScalarSubquery)):
            raise UnsupportedSQL("nested subqueries are not supported")
        if isinstance(expr, Star):
            raise UnsupportedSQL("'*' is only supported as a top-level SELECT item")
        raise UnsupportedSQL(f"unsupported expression {type(expr).__name__}")

    def bind_column(self, expr, scopes):
        name = expr.name.lower()
        if expr.qualifier is None:
            matches = []
            for scope in reversed(scopes):
                for column in scope.columns:
                    if column.lower() == name:
                        matches.append(BColumn(scope.level, scope.alias, column))
                if matches:
                    break
            if not matches:
                raise BindError(f"unknown column {expr.name!r}")
            if len(matches) > 1:
                raise BindError(f"ambiguous column {expr.name!r}")
            return matches[0]

        alias = expr.qualifier.lower()
        matches = [scope for scope in scopes if scope.alias == alias]
        if not matches:
            raise BindError(f"unknown table alias {expr.qualifier!r}")
        scope = matches[-1]
        if not any(column.lower() == name for column in scope.columns):
            raise BindError(f"unknown column {expr.qualifier}.{expr.name}")
        return BColumn(scope.level, scope.alias, next(column for column in scope.columns if column.lower() == name))

    def resolve_star(self, star, scopes):
        if star.qualifier is None:
            return tuple(
                (scope.level, scope.alias, column)
                for scope in scopes
                for column in scope.columns
            )
        alias = star.qualifier.lower()
        matches = [scope for scope in scopes if scope.alias == alias]
        if not matches:
            raise BindError(f"unknown table alias {star.qualifier!r}")
        scope = matches[-1]
        return tuple((scope.level, scope.alias, column) for column in scope.columns)

    def default_name(self, expr):
        if isinstance(expr, ColumnRef):
            return expr.name
        if isinstance(expr, ScalarSubquery):
            return "scalar_subquery"
        return "expr"


def references_level(expr, level):
    if isinstance(expr, BColumn):
        return expr.level == level
    if isinstance(expr, (BUnary, BMarker)):
        return getattr(expr, "expr", None) is not None and references_level(expr.expr, level)
    if isinstance(expr, BBinary):
        return references_level(expr.left, level) or references_level(expr.right, level)
    return False


def min_column_level(expr, default=None):
    levels = []

    def visit(node):
        if isinstance(node, BColumn):
            levels.append(node.level)
        elif isinstance(node, BUnary) and hasattr(node, "expr"):
            visit(node.expr)
        elif isinstance(node, BBinary):
            visit(node.left)
            visit(node.right)

    visit(expr)
    return min(levels) if levels else default


def column_levels(expr):
    levels = []

    def visit(node):
        if isinstance(node, BColumn):
            levels.append(node.level)
        elif isinstance(node, BUnary) and hasattr(node, "expr"):
            visit(node.expr)
        elif isinstance(node, BBinary):
            visit(node.left)
            visit(node.right)

    visit(expr)
    return levels
