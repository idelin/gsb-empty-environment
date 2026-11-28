import { FALSE, TRUE, UNKNOWN, and, col, eqNullSafe, isNotNull, isNull, literal, not, or, } from "./ast.js";
export class QueryExecutionError extends Error {
    constructor(message) {
        super(message);
        this.name = "QueryExecutionError";
    }
}
export class CardinalityViolationError extends QueryExecutionError {
    code = "CARDINALITY_VIOLATION";
    actualRows;
    constructor(actualRows, detail = "") {
        super(`Scalar subquery cardinality violation: expected at most 1 row for one outer row, got ${actualRows}` +
            (detail ? ` (${detail})` : ""));
        this.name = "CardinalityViolationError";
        this.actualRows = actualRows;
    }
}
export class MissingTableError extends QueryExecutionError {
    constructor(name) {
        super(`Table "${name}" is not present in the execution catalog`);
        this.name = "MissingTableError";
    }
}
export class AmbiguousColumnError extends QueryExecutionError {
    constructor(name, matches) {
        super(`Column "${name}" is ambiguous: ${matches.join(", ")}`);
        this.name = "AmbiguousColumnError";
    }
}
export function normalizeContext(context) {
    if (context === undefined)
        return {};
    if ("catalog" in context ||
        "tables" in context ||
        "schema" in context) {
        return context;
    }
    return { catalog: context };
}
function catalogOf(context) {
    return context.catalog ?? context.tables ?? {};
}
function tableData(name, context) {
    const source = catalogOf(context)[name];
    if (source === undefined)
        throw new MissingTableError(name);
    if (Array.isArray(source)) {
        const rows = source;
        const configured = context.schema?.[name];
        const columns = configured ??
            Array.from(new Set(rows.flatMap((row) => Object.keys(row))));
        return { columns, rows };
    }
    return source;
}
function scanRows(name, alias, inlineColumns, inlineRows, context) {
    let columns;
    let rows;
    if (inlineRows !== undefined) {
        rows = inlineRows;
        columns =
            inlineColumns ??
                Array.from(new Set(rows.flatMap((row) => Object.keys(row))));
    }
    else if (inlineColumns !== undefined) {
        columns = inlineColumns;
        rows = [];
    }
    else {
        const data = tableData(name, context);
        columns = data.columns;
        rows = data.rows;
    }
    const qualifiedColumns = columns.map((columnName) => `${alias}.${columnName}`);
    const qualifiedRows = rows.map((row) => {
        const out = {};
        for (const columnName of columns) {
            const qualified = `${alias}.${columnName}`;
            out[qualified] = Object.prototype.hasOwnProperty.call(row, qualified)
                ? row[qualified]
                : (row[columnName] ?? null);
        }
        return out;
    });
    return { columns: qualifiedColumns, rows: qualifiedRows };
}
function groupKeysOf(plan) {
    return plan.groupBy;
}
export function schemaOf(plan, contextLike) {
    const context = normalizeContext(contextLike);
    switch (plan.kind) {
        case "table":
            return scanRows(plan.name, plan.alias ?? plan.name, plan.columns, plan.rows, context).columns.slice();
        case "values":
            return plan.columns.slice();
        case "filter":
        case "distinct":
        case "limit":
            return schemaOf(plan.input, context);
        case "project":
            return plan.columns.map((column) => column.as);
        case "join":
        case "singleJoin":
            return uniqueColumns([
                ...schemaOf(plan.left, context),
                ...schemaOf(plan.right, context),
            ]);
        case "markJoin":
            return [...schemaOf(plan.left, context), plan.marker];
        case "aggregate":
            return [
                ...groupKeysOf(plan).map((key) => key.as),
                ...plan.aggregates.map((aggregate) => aggregate.as),
            ];
        case "union":
            return schemaOf(plan.left, context);
        case "rowNumber":
            return [...schemaOf(plan.input, context), plan.as];
        default: {
            const neverPlan = plan;
            throw new QueryExecutionError(`Unknown plan node ${String(neverPlan)}`);
        }
    }
}
function uniqueColumns(columns) {
    return Array.from(new Set(columns));
}
function getOwn(row, key) {
    if (Object.prototype.hasOwnProperty.call(row, key)) {
        return { found: true, value: row[key] };
    }
    return { found: false, value: null };
}
function lookupInRow(row, name, relation) {
    if (relation !== undefined) {
        return getOwn(row, `${relation}.${name}`);
    }
    const exact = getOwn(row, name);
    if (exact.found)
        return exact;
    const suffix = `.${name}`;
    const matches = Object.keys(row).filter((key) => key.endsWith(suffix));
    if (matches.length === 1) {
        return { found: true, value: row[matches[0]] };
    }
    if (matches.length > 1)
        throw new AmbiguousColumnError(name, matches);
    return { found: false, value: null };
}
function lookupColumn(name, relation, row, outerEnv) {
    const current = lookupInRow(row, name, relation);
    if (current.found)
        return current.value;
    for (const outer of outerEnv) {
        const found = lookupInRow(outer, name, relation);
        if (found.found)
            return found.value;
    }
    throw new QueryExecutionError(`Column ${relation ? `${relation}.` : ""}${name} is not visible`);
}
function asTruth(value) {
    if (value === null || value === undefined)
        return null;
    if (typeof value === "boolean")
        return value;
    if (typeof value === "number" || typeof value === "bigint") {
        return value !== 0;
    }
    throw new QueryExecutionError(`Value ${String(value)} is not boolean`);
}
export function andTruth(left, right) {
    if (left === false || right === false)
        return false;
    if (left === true && right === true)
        return true;
    return null;
}
export function orTruth(left, right) {
    if (left === true || right === true)
        return true;
    if (left === false && right === false)
        return false;
    return null;
}
export function notTruth(value) {
    return value === null ? null : !value;
}
function comparable(value) {
    if (value === null)
        throw new QueryExecutionError("NULL is not comparable");
    return value;
}
function compareRaw(left, right) {
    const a = comparable(left);
    const b = comparable(right);
    if (typeof a === "number" && typeof b === "number") {
        return a < b ? -1 : a > b ? 1 : 0;
    }
    if (typeof a === "bigint" && typeof b === "bigint") {
        return a < b ? -1 : a > b ? 1 : 0;
    }
    if (typeof a === "string" && typeof b === "string") {
        return a < b ? -1 : a > b ? 1 : 0;
    }
    if (typeof a === "boolean" && typeof b === "boolean") {
        return a === b ? 0 : a ? 1 : -1;
    }
    const leftString = String(a);
    const rightString = String(b);
    return leftString < rightString ? -1 : leftString > rightString ? 1 : 0;
}
function sqlEquals(left, right) {
    if (left === null || right === null || left === undefined || right === undefined) {
        return null;
    }
    return left === right || compareRaw(left, right) === 0;
}
export function compareSql(operator, left, right) {
    if (left === null || right === null || left === undefined || right === undefined) {
        return null;
    }
    switch (operator) {
        case "=":
            return sqlEquals(left, right);
        case "<>":
            return notTruth(sqlEquals(left, right));
        case "<":
            return compareRaw(left, right) < 0;
        case "<=":
            return compareRaw(left, right) <= 0;
        case ">":
            return compareRaw(left, right) > 0;
        case ">=":
            return compareRaw(left, right) >= 0;
        default: {
            const neverOp = operator;
            throw new QueryExecutionError(`Unknown comparison ${String(neverOp)}`);
        }
    }
}
function numeric(value) {
    if (value === null)
        return 0;
    if (typeof value === "number")
        return value;
    if (typeof value === "bigint")
        return Number(value);
    if (typeof value === "boolean")
        return value ? 1 : 0;
    const parsed = Number(value);
    if (Number.isNaN(parsed))
        throw new QueryExecutionError(`${String(value)} is not numeric`);
    return parsed;
}
function callFunction(name, args) {
    switch (name) {
        case "lower":
            return args[0] === null ? null : String(args[0]).toLowerCase();
        case "upper":
            return args[0] === null ? null : String(args[0]).toUpperCase();
        case "length":
            return args[0] === null ? null : String(args[0]).length;
        case "abs":
            return args[0] === null ? null : Math.abs(numeric(args[0]));
        case "round":
            return args[0] === null
                ? null
                : Math.round(numeric(args[0]) * 10 ** numeric(args[1] ?? 0)) /
                    10 ** numeric(args[1] ?? 0);
        case "nullif":
            return sqlEquals(args[0] ?? null, args[1] ?? null) === true
                ? null
                : (args[0] ?? null);
        default:
            throw new QueryExecutionError(`Unsupported function ${name}`);
    }
}
function evalExprInternal(expr, row, outerEnv, context) {
    switch (expr.kind) {
        case "column":
            return lookupColumn(expr.name, expr.relation, row, outerEnv);
        case "literal":
            return expr.value;
        case "binary": {
            const left = evalExprInternal(expr.left, row, outerEnv, context);
            if (expr.op === "and") {
                const leftTruth = asTruth(left);
                if (leftTruth === false)
                    return false;
                return andTruth(leftTruth, asTruth(evalExprInternal(expr.right, row, outerEnv, context)));
            }
            if (expr.op === "or") {
                const leftTruth = asTruth(left);
                if (leftTruth === true)
                    return true;
                return orTruth(leftTruth, asTruth(evalExprInternal(expr.right, row, outerEnv, context)));
            }
            const right = evalExprInternal(expr.right, row, outerEnv, context);
            if (expr.op === "is distinct from") {
                return !isNotDistinctFrom(left, right);
            }
            if (expr.op === "is not distinct from") {
                return isNotDistinctFrom(left, right);
            }
            if (expr.op === "=" || expr.op === "<>" || expr.op === "<" ||
                expr.op === "<=" || expr.op === ">" || expr.op === ">=") {
                return compareSql(expr.op, left, right);
            }
            if (left === null || right === null)
                return null;
            switch (expr.op) {
                case "+":
                    return numeric(left) + numeric(right);
                case "-":
                    return numeric(left) - numeric(right);
                case "*":
                    return numeric(left) * numeric(right);
                case "/":
                    return numeric(left) / numeric(right);
                case "%":
                    return numeric(left) % numeric(right);
                case "||":
                    return `${String(left)}${String(right)}`;
                default: {
                    const neverOp = expr.op;
                    throw new QueryExecutionError(`Unsupported operator ${String(neverOp)}`);
                }
            }
        }
        case "unary": {
            const value = evalExprInternal(expr.arg, row, outerEnv, context);
            switch (expr.op) {
                case "not":
                    return notTruth(asTruth(value));
                case "neg":
                    return value === null ? null : -numeric(value);
                case "is null":
                    return value === null;
                case "is not null":
                    return value !== null;
                case "is true":
                    return asTruth(value) === true;
                case "is false":
                    return asTruth(value) === false;
                case "is unknown":
                    return asTruth(value) === null;
            }
        }
        case "case": {
            for (const branch of expr.whens) {
                if (asTruth(evalExprInternal(branch.when, row, outerEnv, context)) === true) {
                    return evalExprInternal(branch.then, row, outerEnv, context);
                }
            }
            return expr.otherwise === undefined
                ? null
                : evalExprInternal(expr.otherwise, row, outerEnv, context);
        }
        case "coalesce":
            for (const arg of expr.args) {
                const value = evalExprInternal(arg, row, outerEnv, context);
                if (value !== null)
                    return value;
            }
            return null;
        case "call":
            return callFunction(expr.name, expr.args.map((arg) => evalExprInternal(arg, row, outerEnv, context)));
        case "subquery":
            return evalSubquery(expr, row, outerEnv, context);
        default: {
            const neverExpr = expr;
            throw new QueryExecutionError(`Unknown expression ${String(neverExpr)}`);
        }
    }
}
function isNotDistinctFrom(left, right) {
    if (left === null || left === undefined) {
        return right === null || right === undefined;
    }
    if (right === null || right === undefined)
        return false;
    return sqlEquals(left, right) === true;
}
export function evaluateExpression(expr, row, contextLike, outerEnv = []) {
    return evalExprInternal(expr, row, outerEnv, normalizeContext(contextLike));
}
function negate(value) {
    return notTruth(value);
}
function quantify(quantifier, results) {
    if (quantifier === "any") {
        if (results.some((value) => value === true))
            return true;
        if (results.some((value) => value === null))
            return null;
        return false;
    }
    if (results.some((value) => value === false))
        return false;
    if (results.some((value) => value === null))
        return null;
    return true;
}
function evalSubquery(expr, row, outerEnv, context) {
    const rows = evalRelInternal(expr.query, context, [row, ...outerEnv]);
    const columns = schemaOf(expr.query, context);
    if (expr.mode === "exists") {
        const existsValue = rows.length > 0;
        return expr.negated ? !existsValue : existsValue;
    }
    if (expr.mode === "scalar") {
        if (rows.length > 1)
            throw new CardinalityViolationError(rows.length);
        if (rows.length === 0)
            return null;
        return columns.length === 0 ? null : rows[0][columns[0]];
    }
    if (expr.left === undefined) {
        throw new QueryExecutionError(`${expr.mode} subquery requires a left expression`);
    }
    const target = columns[0];
    if (target === undefined) {
        throw new QueryExecutionError("Comparison subquery must project a column");
    }
    const left = evalExprInternal(expr.left, row, outerEnv, context);
    const results = rows.map((rightRow) => {
        const right = evalExprInternal({ kind: "column", name: target }, rightRow, [row, ...outerEnv], context);
        return compareSql(expr.operator ?? "=", left, right);
    });
    const raw = quantify(expr.mode === "all" ? "all" : "any", results);
    return expr.negated ? negate(raw) : raw;
}
function keyOf(values) {
    return JSON.stringify(values.map((value) => {
        if (typeof value === "bigint")
            return ["bigint", value.toString()];
        return [typeof value, value];
    }));
}
function distinctValues(values) {
    const seen = new Set();
    const out = [];
    for (const value of values) {
        const key = keyOf([value]);
        if (!seen.has(key)) {
            seen.add(key);
            out.push(value);
        }
    }
    return out;
}
function aggregateValue(call, rows, outerEnv, context) {
    if (call.fn === "count" && call.arg === undefined)
        return rows.length;
    if (call.arg === undefined)
        return null;
    let values = rows
        .map((row) => evalExprInternal(call.arg, row, outerEnv, context))
        .filter((value) => value !== null);
    if (call.distinct)
        values = distinctValues(values);
    if (call.fn === "count")
        return values.length;
    if (values.length === 0)
        return null;
    switch (call.fn) {
        case "sum":
            return values.reduce((total, value) => total + numeric(value), 0);
        case "avg":
            return (values.reduce((total, value) => total + numeric(value), 0) /
                values.length);
        case "min":
            return values.reduce((best, value) => compareRaw(value, best) < 0 ? value : best);
        case "max":
            return values.reduce((best, value) => compareRaw(value, best) > 0 ? value : best);
        default: {
            const neverFn = call.fn;
            throw new QueryExecutionError(`Unknown aggregate ${String(neverFn)}`);
        }
    }
}
function nullExtend(row, columns) {
    const out = { ...row };
    for (const columnName of columns)
        out[columnName] = null;
    return out;
}
function sortRows(rows, orderBy, outerEnv, context) {
    return rows
        .map((row, index) => ({ row, index }))
        .sort((left, right) => {
        for (const key of orderBy) {
            const a = evalExprInternal(key.expr, left.row, outerEnv, context);
            const b = evalExprInternal(key.expr, right.row, outerEnv, context);
            const direction = key.direction ?? "asc";
            const nulls = key.nulls ?? "last";
            if (a === null || b === null) {
                if (a === null && b === null)
                    continue;
                const nullFirst = nulls === "first";
                return a === null ? (nullFirst ? -1 : 1) : nullFirst ? 1 : -1;
            }
            const compared = compareRaw(a, b) * (direction === "desc" ? -1 : 1);
            if (compared !== 0)
                return compared;
        }
        return left.index - right.index;
    })
        .map((entry) => entry.row);
}
function evalRelInternal(plan, context, outerEnv) {
    switch (plan.kind) {
        case "table": {
            const alias = plan.alias ?? plan.name;
            const data = scanRows(plan.name, alias, plan.columns, plan.rows, context);
            return data.rows.map((row) => ({ ...row }));
        }
        case "values":
            return plan.rows.map((row) => ({ ...row }));
        case "filter":
            return evalRelInternal(plan.input, context, outerEnv).filter((row) => asTruth(evalExprInternal(plan.predicate, row, outerEnv, context)) === true);
        case "project":
            return evalRelInternal(plan.input, context, outerEnv).map((row) => {
                const out = {};
                for (const column of plan.columns) {
                    out[column.as] = evalExprInternal(column.expr, row, outerEnv, context);
                }
                return out;
            });
        case "join":
            return evalJoin(plan, context, outerEnv);
        case "markJoin":
            return evalMarkJoin(plan, context, outerEnv);
        case "singleJoin":
            return evalSingleJoin(plan, context, outerEnv);
        case "aggregate":
            return evalAggregate(plan, context, outerEnv);
        case "union": {
            const left = evalRelInternal(plan.left, context, outerEnv);
            const rightRows = evalRelInternal(plan.right, context, outerEnv);
            const leftColumns = schemaOf(plan.left, context);
            const rightColumns = schemaOf(plan.right, context);
            if (leftColumns.length !== rightColumns.length) {
                throw new QueryExecutionError(`UNION column count mismatch: ${leftColumns.length} vs ${rightColumns.length}`);
            }
            const aligned = rightRows.map((row) => {
                const out = {};
                for (const [index, name] of leftColumns.entries()) {
                    out[name] = row[rightColumns[index]];
                }
                return out;
            });
            const rows = [...left, ...aligned];
            if (plan.all)
                return rows;
            const seen = new Set();
            return rows.filter((row) => {
                const key = keyOf(leftColumns.map((name) => row[name]));
                if (seen.has(key))
                    return false;
                seen.add(key);
                return true;
            });
        }
        case "distinct": {
            const rows = evalRelInternal(plan.input, context, outerEnv);
            const columns = schemaOf(plan.input, context);
            const seen = new Set();
            return rows.filter((row) => {
                const key = keyOf(columns.map((name) => row[name]));
                if (seen.has(key))
                    return false;
                seen.add(key);
                return true;
            });
        }
        case "limit": {
            let rows = evalRelInternal(plan.input, context, outerEnv);
            if (plan.orderBy !== undefined && plan.orderBy.length > 0) {
                rows = sortRows(rows, plan.orderBy, outerEnv, context);
            }
            const offset = plan.offset ?? 0;
            return rows.slice(offset, offset + plan.count);
        }
        case "rowNumber":
            return evalRowNumber(plan, context, outerEnv);
        default: {
            const neverPlan = plan;
            throw new QueryExecutionError(`Unknown plan node ${String(neverPlan)}`);
        }
    }
}
function evalJoin(plan, context, outerEnv) {
    const leftRows = evalRelInternal(plan.left, context, outerEnv);
    const rightRows = evalRelInternal(plan.right, context, outerEnv);
    const rightColumns = schemaOf(plan.right, context);
    const leftColumns = schemaOf(plan.left, context);
    const out = [];
    if (plan.joinType === "inner" || plan.joinType === "left") {
        const rightMatched = new Set();
        for (const leftRow of leftRows) {
            let matched = false;
            for (const [index, rightRow] of rightRows.entries()) {
                const merged = { ...leftRow, ...rightRow };
                if (asTruth(evalExprInternal(plan.condition, merged, outerEnv, context)) === true) {
                    out.push(merged);
                    matched = true;
                    rightMatched.add(index);
                }
            }
            if (!matched && plan.joinType === "left") {
                out.push(nullExtend(leftRow, rightColumns));
            }
        }
        return out;
    }
    const leftMatched = new Set();
    for (const [rightIndex, rightRow] of rightRows.entries()) {
        let matched = false;
        for (const [leftIndex, leftRow] of leftRows.entries()) {
            const merged = { ...leftRow, ...rightRow };
            if (asTruth(evalExprInternal(plan.condition, merged, outerEnv, context)) === true) {
                out.push(merged);
                matched = true;
                leftMatched.add(leftIndex);
            }
        }
        if (!matched && (plan.joinType === "right" || plan.joinType === "full")) {
            out.push(nullExtend(rightRow, leftColumns));
        }
    }
    if (plan.joinType === "full") {
        for (const [index, leftRow] of leftRows.entries()) {
            if (!leftMatched.has(index))
                out.push(nullExtend(leftRow, rightColumns));
        }
    }
    return out;
}
function evalMarkJoin(plan, context, outerEnv) {
    const leftRows = evalRelInternal(plan.left, context, outerEnv);
    const rightRows = evalRelInternal(plan.right, context, outerEnv);
    return leftRows.map((leftRow) => {
        const matched = rightRows.filter((rightRow) => {
            const merged = { ...leftRow, ...rightRow };
            return (asTruth(evalExprInternal(plan.condition, merged, outerEnv, context)) === true);
        });
        let marker;
        if (plan.semantics.quantifier === "exists") {
            marker = matched.length > 0;
        }
        else {
            const leftExpr = plan.semantics.left;
            const rightExpr = plan.semantics.right;
            if (leftExpr === undefined || rightExpr === undefined) {
                throw new QueryExecutionError("ANY/ALL MarkJoin requires left and right expressions");
            }
            const left = evalExprInternal(leftExpr, leftRow, outerEnv, context);
            const comparisons = matched.map((rightRow) => compareSql(plan.semantics.operator ?? "=", left, evalExprInternal(rightExpr, rightRow, [leftRow, ...outerEnv], context)));
            marker = quantify(plan.semantics.quantifier === "all" ? "all" : "any", comparisons);
        }
        if (plan.semantics.negated)
            marker = negate(marker);
        return { ...leftRow, [plan.marker]: marker };
    });
}
function evalSingleJoin(plan, context, outerEnv) {
    const leftRows = evalRelInternal(plan.left, context, outerEnv);
    const rightRows = evalRelInternal(plan.right, context, outerEnv);
    const rightColumns = schemaOf(plan.right, context);
    return leftRows.map((leftRow) => {
        const matched = rightRows.filter((rightRow) => {
            const merged = { ...leftRow, ...rightRow };
            return (asTruth(evalExprInternal(plan.condition, merged, outerEnv, context)) === true);
        });
        if (matched.length > 1)
            throw new CardinalityViolationError(matched.length);
        return matched.length === 0
            ? nullExtend(leftRow, rightColumns)
            : { ...leftRow, ...matched[0] };
    });
}
function evalAggregate(plan, context, outerEnv) {
    const rows = evalRelInternal(plan.input, context, outerEnv);
    const groupKeys = groupKeysOf(plan);
    const groups = new Map();
    for (const row of rows) {
        const keys = groupKeys.map((key) => evalExprInternal(key.expr, row, outerEnv, context));
        const groupKey = keyOf(keys);
        const group = groups.get(groupKey);
        if (group === undefined) {
            groups.set(groupKey, { keys, rows: [row] });
        }
        else {
            group.rows.push(row);
        }
    }
    if (groupKeys.length === 0 && groups.size === 0) {
        groups.set(keyOf([]), { keys: [], rows: [] });
    }
    const out = [];
    for (const group of groups.values()) {
        const row = {};
        for (const [index, key] of groupKeys.entries()) {
            row[key.as] = group.keys[index];
        }
        for (const aggregate of plan.aggregates) {
            row[aggregate.as] = aggregateValue(aggregate, group.rows, outerEnv, context);
        }
        out.push(row);
    }
    return out;
}
function evalRowNumber(plan, context, outerEnv) {
    const rows = evalRelInternal(plan.input, context, outerEnv);
    const partitions = new Map();
    for (const [index, row] of rows.entries()) {
        const values = plan.partitionBy.map((expr) => evalExprInternal(expr, row, outerEnv, context));
        const key = keyOf(values);
        const partition = partitions.get(key);
        if (partition === undefined)
            partitions.set(key, [{ row, index }]);
        else
            partition.push({ row, index });
    }
    const numbers = new Map();
    for (const partition of partitions.values()) {
        const sorted = [...partition].sort((left, right) => {
            for (const key of plan.orderBy) {
                const a = evalExprInternal(key.expr, left.row, outerEnv, context);
                const b = evalExprInternal(key.expr, right.row, outerEnv, context);
                if (a === null || b === null) {
                    if (a === null && b === null)
                        continue;
                    const nullFirst = (key.nulls ?? "last") === "first";
                    return a === null ? (nullFirst ? -1 : 1) : nullFirst ? 1 : -1;
                }
                const compared = compareRaw(a, b) * ((key.direction ?? "asc") === "desc" ? -1 : 1);
                if (compared !== 0)
                    return compared;
            }
            return left.index - right.index;
        });
        for (const [position, entry] of sorted.entries()) {
            numbers.set(entry.index, position + 1);
        }
    }
    return rows.map((row, index) => ({
        ...row,
        [plan.as]: numbers.get(index) ?? 1,
    }));
}
export function evaluatePlan(plan, contextLike) {
    const context = normalizeContext(contextLike);
    const columns = schemaOf(plan, context);
    const rows = evalRelInternal(plan, context, []);
    return {
        columns,
        rows: rows.map((row) => {
            const out = {};
            for (const column of columns)
                out[column] = row[column] ?? null;
            return out;
        }),
    };
}
export const execute = evaluatePlan;
export const executeReference = evaluatePlan;
export const executeNested = evaluatePlan;
export const executeQuery = evaluatePlan;
export const executePlan = evaluatePlan;
export const runReference = evaluatePlan;
export const runNested = evaluatePlan;
export const runQuery = evaluatePlan;
export class ReferenceExecutor {
    contextLike;
    constructor(contextLike) {
        this.contextLike = contextLike;
    }
    execute(plan) {
        return evaluatePlan(plan, this.contextLike);
    }
}
export function evaluateRows(plan, contextLike) {
    return evaluatePlan(plan, contextLike).rows.slice();
}
export function walkPlan(plan, visit) {
    visit(plan);
    switch (plan.kind) {
        case "table":
        case "values":
            return;
        case "filter":
            walkExpr(plan.predicate, (expr) => {
                if (expr.kind === "subquery")
                    walkPlan(expr.query, visit);
            });
            walkPlan(plan.input, visit);
            return;
        case "project":
            for (const column of plan.columns) {
                walkExpr(column.expr, (expr) => {
                    if (expr.kind === "subquery")
                        walkPlan(expr.query, visit);
                });
            }
            walkPlan(plan.input, visit);
            return;
        case "join":
        case "markJoin":
        case "singleJoin":
            walkExpr(plan.condition, (expr) => {
                if (expr.kind === "subquery")
                    walkPlan(expr.query, visit);
            });
            if (plan.kind === "markJoin") {
                if (plan.semantics.left !== undefined) {
                    walkExpr(plan.semantics.left, (expr) => {
                        if (expr.kind === "subquery")
                            walkPlan(expr.query, visit);
                    });
                }
                if (plan.semantics.right !== undefined) {
                    walkExpr(plan.semantics.right, (expr) => {
                        if (expr.kind === "subquery")
                            walkPlan(expr.query, visit);
                    });
                }
            }
            walkPlan(plan.left, visit);
            walkPlan(plan.right, visit);
            return;
        case "aggregate":
            for (const key of groupKeysOf(plan)) {
                walkExpr(key.expr, (expr) => {
                    if (expr.kind === "subquery")
                        walkPlan(expr.query, visit);
                });
            }
            for (const aggregate of plan.aggregates) {
                if (aggregate.arg !== undefined) {
                    walkExpr(aggregate.arg, (expr) => {
                        if (expr.kind === "subquery")
                            walkPlan(expr.query, visit);
                    });
                }
            }
            walkPlan(plan.input, visit);
            return;
        case "union":
            walkPlan(plan.left, visit);
            walkPlan(plan.right, visit);
            return;
        case "distinct":
        case "limit":
            if (plan.kind === "limit" && plan.orderBy !== undefined) {
                for (const key of plan.orderBy) {
                    walkExpr(key.expr, (expr) => {
                        if (expr.kind === "subquery")
                            walkPlan(expr.query, visit);
                    });
                }
            }
            walkPlan(plan.input, visit);
            return;
        case "rowNumber":
            for (const expr of plan.partitionBy) {
                walkExpr(expr, (sub) => {
                    if (sub.kind === "subquery")
                        walkPlan(sub.query, visit);
                });
            }
            for (const key of plan.orderBy) {
                walkExpr(key.expr, (sub) => {
                    if (sub.kind === "subquery")
                        walkPlan(sub.query, visit);
                });
            }
            walkPlan(plan.input, visit);
            return;
        default: {
            const neverPlan = plan;
            throw new QueryExecutionError(`Unknown plan node ${String(neverPlan)}`);
        }
    }
}
export function walkExpr(expr, visit) {
    visit(expr);
    switch (expr.kind) {
        case "column":
        case "literal":
            return;
        case "binary":
            walkExpr(expr.left, visit);
            walkExpr(expr.right, visit);
            return;
        case "unary":
            walkExpr(expr.arg, visit);
            return;
        case "case":
            for (const branch of expr.whens) {
                walkExpr(branch.when, visit);
                walkExpr(branch.then, visit);
            }
            if (expr.otherwise !== undefined)
                walkExpr(expr.otherwise, visit);
            return;
        case "coalesce":
            for (const arg of expr.args)
                walkExpr(arg, visit);
            return;
        case "call":
            for (const arg of expr.args)
                walkExpr(arg, visit);
            return;
        case "subquery":
            if (expr.left !== undefined)
                walkExpr(expr.left, visit);
            return;
        default: {
            const neverExpr = expr;
            throw new QueryExecutionError(`Unknown expression ${String(neverExpr)}`);
        }
    }
}
export function containsSubquery(plan) {
    let found = false;
    walkPlan(plan, (node) => {
        const expressions = [];
        switch (node.kind) {
            case "filter":
                expressions.push(node.predicate);
                break;
            case "project":
                expressions.push(...node.columns.map((column) => column.expr));
                break;
            case "join":
            case "markJoin":
            case "singleJoin":
                expressions.push(node.condition);
                break;
            case "aggregate":
                expressions.push(...groupKeysOf(node).map((key) => key.expr));
                expressions.push(...node.aggregates.flatMap((aggregate) => aggregate.arg === undefined ? [] : [aggregate.arg]));
                break;
            default:
                break;
        }
        for (const expr of expressions) {
            walkExpr(expr, (sub) => {
                if (sub.kind === "subquery")
                    found = true;
            });
        }
    });
    return found;
}
export function assertFullyDecorrelated(plan) {
    if (containsSubquery(plan)) {
        throw new QueryExecutionError("Plan still contains a subquery expression after unnesting");
    }
}
export function isNullValue(value) {
    return value === null;
}
export const sqlAnd = and;
export const sqlOr = or;
export const sqlNot = not;
export const sqlIsNull = isNull;
export const sqlIsNotNull = isNotNull;
export const sqlEq = (left, right) => binaryEq(left, right);
export const sqlEqNullSafe = eqNullSafe;
export const sqlCol = col;
export const sqlLiteral = literal;
export const sqlTrue = TRUE;
export const sqlFalse = FALSE;
export const sqlUnknown = UNKNOWN;
function binaryEq(left, right) {
    return { kind: "binary", op: "=", left, right };
}
