export const TRUE = true;
export const FALSE = false;
export const UNKNOWN = null;
export function col(first, second) {
    if (second !== undefined) {
        return { kind: "column", relation: first, name: second };
    }
    const dot = first.indexOf(".");
    if (dot > 0 && dot < first.length - 1) {
        return {
            kind: "column",
            relation: first.slice(0, dot),
            name: first.slice(dot + 1),
        };
    }
    return { kind: "column", name: first };
}
export const column = col;
export function literal(value) {
    return { kind: "literal", value };
}
export const lit = literal;
export function binary(op, left, right) {
    return { kind: "binary", op, left, right };
}
export function unary(op, arg) {
    return { kind: "unary", op, arg };
}
export const eq = (left, right) => binary("=", left, right);
export const neq = (left, right) => binary("<>", left, right);
export const lt = (left, right) => binary("<", left, right);
export const lte = (left, right) => binary("<=", left, right);
export const gt = (left, right) => binary(">", left, right);
export const gte = (left, right) => binary(">=", left, right);
export const add = (left, right) => binary("+", left, right);
export const sub = (left, right) => binary("-", left, right);
export const mul = (left, right) => binary("*", left, right);
export const div = (left, right) => binary("/", left, right);
export const eqNullSafe = (left, right) => binary("is not distinct from", left, right);
export function and(...args) {
    if (args.length === 0)
        return literal(true);
    return args.slice(1).reduce((left, right) => binary("and", left, right), args[0]);
}
export function or(...args) {
    if (args.length === 0)
        return literal(false);
    return args.slice(1).reduce((left, right) => binary("or", left, right), args[0]);
}
export const not = (arg) => unary("not", arg);
export const isNull = (arg) => unary("is null", arg);
export const isNotNull = (arg) => unary("is not null", arg);
export const neg = (arg) => unary("neg", arg);
export function caseWhen(whens, otherwise) {
    return { kind: "case", whens, otherwise };
}
export function coalesce(...args) {
    return { kind: "coalesce", args };
}
export function call(name, ...args) {
    return { kind: "call", name: name.toLowerCase(), args };
}
export function table(name, alias) {
    return alias === undefined
        ? { kind: "table", name }
        : { kind: "table", name, alias };
}
export function values(columns, rows) {
    return { kind: "values", columns, rows };
}
export function filter(predicate, input) {
    return { kind: "filter", input, predicate };
}
function isPlan(value) {
    return (typeof value === "object" &&
        value !== null &&
        "kind" in value &&
        typeof value.kind === "string");
}
export function project(first, second) {
    if (isPlan(first) && Array.isArray(second)) {
        return { kind: "project", input: first, columns: second };
    }
    if (Array.isArray(first) && isPlan(second)) {
        return { kind: "project", input: second, columns: first };
    }
    throw new TypeError("project expects a plan and a column list");
}
export function selectColumn(as, expr) {
    return { as, expr };
}
export function join(left, right, condition = literal(true), joinType = "inner") {
    return { kind: "join", joinType, left, right, condition };
}
export function leftJoin(left, right, condition = literal(true)) {
    return join(left, right, condition, "left");
}
export function markJoin(left, right, condition, marker, semantics) {
    return { kind: "markJoin", left, right, condition, marker, semantics };
}
export function singleJoin(left, right, condition = literal(true)) {
    return { kind: "singleJoin", left, right, condition };
}
export function group(as, expr) {
    return { as, expr };
}
function normalizeGroupKeys(keys) {
    const out = [];
    for (const [index, key] of keys.entries()) {
        if ("kind" in key) {
            const expr = key;
            const name = expr.kind === "column"
                ? expr.name
                : expr.kind === "literal"
                    ? `group${index}`
                    : `group${index}`;
            out.push({ as: name, expr });
        }
        else {
            out.push(key);
        }
    }
    return out;
}
export function aggregate(input, groupBy, aggregates) {
    return {
        kind: "aggregate",
        input,
        groupBy: normalizeGroupKeys(groupBy),
        aggregates,
    };
}
export function agg(fn, as, arg, distinct = false) {
    return arg === undefined
        ? { fn, as, distinct }
        : { fn, as, arg, distinct };
}
export const count = (as = "count", arg, distinct = false) => agg("count", as, arg, distinct);
export const sum = (as = "sum", arg, distinct = false) => agg("sum", as, arg, distinct);
export const avg = (as = "avg", arg, distinct = false) => agg("avg", as, arg, distinct);
export const min = (as = "min", arg, distinct = false) => agg("min", as, arg, distinct);
export const max = (as = "max", arg, distinct = false) => agg("max", as, arg, distinct);
export function union(left, right, all = false) {
    return { kind: "union", all, left, right };
}
export function distinct(input) {
    return { kind: "distinct", input };
}
export function sortKey(expr, direction = "asc", nulls = "last") {
    return { expr, direction, nulls };
}
export function limit(input, countValue, offset = 0, orderBy) {
    return orderBy === undefined
        ? { kind: "limit", input, count: countValue, offset }
        : { kind: "limit", input, count: countValue, offset, orderBy };
}
export function rowNumber(input, partitionBy, orderBy, as = "__row_number") {
    return { kind: "rowNumber", input, partitionBy, orderBy, as };
}
export function scalar(query) {
    return { kind: "subquery", mode: "scalar", query };
}
export function existsSubquery(query) {
    return { kind: "subquery", mode: "exists", query };
}
export function notExistsSubquery(query) {
    return { kind: "subquery", mode: "exists", query, negated: true };
}
export function inSubquery(left, query) {
    return { kind: "subquery", mode: "in", query, left, operator: "=" };
}
export function notInSubquery(left, query) {
    return {
        kind: "subquery",
        mode: "in",
        query,
        left,
        operator: "=",
        negated: true,
    };
}
export function anyCompare(left, operator, query) {
    return { kind: "subquery", mode: "any", query, left, operator };
}
export function allCompare(left, operator, query) {
    return { kind: "subquery", mode: "all", query, left, operator };
}
export const scalarSubquery = scalar;
export const exists = existsSubquery;
export const notExists = notExistsSubquery;
export const inSubqueryExpr = inSubquery;
export const notInSubqueryExpr = notInSubquery;
export const anySubquery = anyCompare;
export const allSubquery = allCompare;
export const isIn = inSubquery;
export const isNotIn = notInSubquery;
export const leftOuterJoin = leftJoin;
export const innerJoin = join;
export const rightJoin = (left, right, condition = literal(true)) => join(left, right, condition, "right");
export const fullJoin = (left, right, condition = literal(true)) => join(left, right, condition, "full");
export const groupBy = group;
export const where = filter;
export const select = project;
export const scan = table;
