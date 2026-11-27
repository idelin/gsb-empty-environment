import { parseJsonPath } from "./parser.js";
import { jsonEqual } from "./evaluator.js";
const allPlan = (reason) => ({ kind: "all", reason });
function termKey(term) {
    return JSON.stringify(term);
}
function andPlan(plans) {
    const children = [];
    const seen = new Set();
    const append = (plan) => {
        if (plan.kind === "all") {
            return;
        }
        if (plan.kind === "and") {
            for (const child of plan.children) {
                append(child);
            }
            return;
        }
        const key = termKeyForPlan(plan);
        if (key !== undefined) {
            if (seen.has(key)) {
                return;
            }
            seen.add(key);
        }
        children.push(plan);
    };
    for (const plan of plans) {
        append(plan);
    }
    if (children.length === 0) {
        return allPlan("no usable index predicate");
    }
    if (children.length === 1) {
        return children[0];
    }
    return { kind: "and", children };
}
function orPlan(plans) {
    const children = [];
    const seen = new Set();
    const append = (plan) => {
        if (plan.kind === "all") {
            return false;
        }
        if (plan.kind === "or") {
            for (const child of plan.children) {
                if (!append(child)) {
                    return false;
                }
            }
            return true;
        }
        const key = termKeyForPlan(plan);
        if (key !== undefined) {
            if (seen.has(key)) {
                return true;
            }
            seen.add(key);
        }
        children.push(plan);
        return true;
    };
    for (const plan of plans) {
        if (!append(plan)) {
            return allPlan("an OR branch has no safe index predicate");
        }
    }
    if (children.length === 0) {
        return allPlan("no usable index predicate");
    }
    if (children.length === 1) {
        return children[0];
    }
    return { kind: "or", children };
}
function termKeyForPlan(plan) {
    if (plan.kind === "term") {
        return termKey(plan.term);
    }
    return undefined;
}
function termExists(path, source) {
    if (path.length === 0 ||
        path.every((segment) => segment.kind === "laxUnwrap")) {
        return allPlan("empty path is always present");
    }
    return {
        kind: "term",
        term: {
            test: "exists",
            path,
            ...(source === undefined ? {} : { source }),
        },
    };
}
function termEquals(path, value, source) {
    return {
        kind: "term",
        term: {
            test: "equals",
            path,
            value,
            ...(source === undefined ? {} : { source }),
        },
    };
}
function pathSegments(path, mode) {
    const segments = [];
    let complete = true;
    for (const step of path.steps) {
        if (step.kind === "filter") {
            if (mode === "lax") {
                segments.push({ kind: "laxUnwrap" });
            }
            continue;
        }
        if (step.kind === "method") {
            complete = false;
            break;
        }
        segments.push(...navigationSegments(step, mode));
    }
    return { segments, complete };
}
function navigationSegments(step, mode) {
    switch (step.kind) {
        case "member":
            return mode === "lax"
                ? [{ kind: "laxUnwrap" }, { kind: "key", key: step.key }]
                : [{ kind: "key", key: step.key }];
        case "memberWildcard":
            return mode === "lax"
                ? [{ kind: "laxUnwrap" }, { kind: "anyKey" }]
                : [{ kind: "anyKey" }];
        case "arrayAccess":
            return [
                {
                    kind: "arraySelection",
                    wildcard: step.wildcard,
                    selectors: step.selectors,
                    ...(mode === "lax" ? { wrapScalar: true } : {}),
                },
            ];
        case "recursive":
            return [{ kind: "recursive" }];
    }
}
function resolvePath(path, prefix, mode) {
    const selected = pathSegments(path, mode);
    if (path.origin === "$") {
        return selected;
    }
    if (prefix === null) {
        return null;
    }
    return {
        segments: [...prefix, ...selected.segments],
        complete: selected.complete,
    };
}
function pathExistsPlan(path, prefix, mode, source) {
    const resolved = resolvePath(path, prefix, mode);
    if (resolved === null) {
        return allPlan("current item is not traceable to the document");
    }
    return termExists(resolved.segments, source);
}
function pathEqualityPlan(path, value, prefix, mode, source) {
    const resolved = resolvePath(path, prefix, mode);
    if (resolved === null) {
        return allPlan("current item is not traceable to the document");
    }
    if (!resolved.complete) {
        return termExists(resolved.segments, source);
    }
    if (mode === "lax") {
        return orPlan([
            termEquals(resolved.segments, value, source),
            termEquals([...resolved.segments, { kind: "anyIndex" }], value, source),
        ]);
    }
    return termEquals(resolved.segments, value, source);
}
function expressionExistencePlan(expression, prefix, mode, source) {
    switch (expression.kind) {
        case "path":
            return pathExistsPlan(expression, prefix, mode, source);
        case "exists":
            return pathExistsPlan(expression.path, prefix, mode, source);
        case "literal":
        case "last":
            return allPlan("literal has no index predicate");
        case "unary":
            return expressionExistencePlan(expression.operand, prefix, mode, source);
        case "binary":
            return andPlan([
                expressionExistencePlan(expression.left, prefix, mode, source),
                expressionExistencePlan(expression.right, prefix, mode, source),
            ]);
        case "likeRegex":
            return expressionExistencePlan(expression.left, prefix, mode, source);
    }
}
function comparisonPlan(operator, left, right, prefix, mode, source) {
    if (operator === "==") {
        const rightConstant = constantValue(right);
        if (left.kind === "path" && rightConstant !== undefined) {
            return pathEqualityPlan(left, rightConstant, prefix, mode, source);
        }
        const leftConstant = constantValue(left);
        if (right.kind === "path" && leftConstant !== undefined) {
            return pathEqualityPlan(right, leftConstant, prefix, mode, source);
        }
    }
    return andPlan([
        expressionExistencePlan(left, prefix, mode, source),
        expressionExistencePlan(right, prefix, mode, source),
    ]);
}
function constantValue(expression) {
    if (expression.kind === "literal") {
        return expression.value;
    }
    if (expression.kind === "unary" &&
        (expression.operator === "+" || expression.operator === "-")) {
        const operand = constantValue(expression.operand);
        if (typeof operand === "number") {
            return expression.operator === "+" ? operand : -operand;
        }
    }
    return undefined;
}
function predicatePlan(predicate, prefix, mode, source) {
    switch (predicate.kind) {
        case "path":
            return pathExistsPlan(predicate, prefix, mode, source);
        case "exists":
            return pathExistsPlan(predicate.path, prefix, mode, source);
        case "unary":
            if (predicate.operator === "!") {
                return allPlan("negated predicates cannot be safely pushed down");
            }
            return expressionExistencePlan(predicate.operand, prefix, mode, source);
        case "binary":
            switch (predicate.operator) {
                case "&&":
                    return andPlan([
                        predicatePlan(predicate.left, prefix, mode, source),
                        predicatePlan(predicate.right, prefix, mode, source),
                    ]);
                case "||":
                    return orPlan([
                        predicatePlan(predicate.left, prefix, mode, source),
                        predicatePlan(predicate.right, prefix, mode, source),
                    ]);
                case "==":
                case "!=":
                case "<":
                case "<=":
                case ">":
                case ">=":
                    return comparisonPlan(predicate.operator, predicate.left, predicate.right, prefix, mode, source);
                default:
                    return expressionExistencePlan(predicate, prefix, mode, source);
            }
        case "likeRegex":
            return expressionExistencePlan(predicate.left, prefix, mode, source);
        case "literal":
        case "last":
            return allPlan("predicate has no safe index predicate");
    }
}
export function extractIndexPlan(input, options = {}) {
    const ast = typeof input === "string"
        ? parseJsonPath(input, { defaultMode: options.mode })
        : input;
    const mode = ast.mode;
    let prefix = [];
    const plans = [];
    for (const step of ast.root.steps) {
        if (step.kind === "filter") {
            const predicatePrefix = mode === "lax" && prefix !== null
                ? [...prefix, { kind: "laxUnwrap" }]
                : prefix;
            plans.push(predicatePlan(step.predicate, predicatePrefix, mode, ast.source));
            if (mode === "lax" && prefix !== null) {
                prefix = [...prefix, { kind: "laxUnwrap" }];
            }
            continue;
        }
        if (step.kind === "method") {
            if (prefix !== null) {
                plans.push(termExists(prefix, ast.source));
            }
            prefix = null;
            continue;
        }
        if (prefix !== null) {
            prefix = [...prefix, ...navigationSegments(step, mode)];
        }
    }
    if (prefix !== null) {
        plans.push(termExists(prefix, ast.source));
    }
    return andPlan(plans);
}
export const extractJsonPathIndexPlan = extractIndexPlan;
export const indexPlan = extractIndexPlan;
export function extractIndexTerms(input) {
    const plan = extractIndexPlan(input);
    const output = [];
    const visit = (current) => {
        if (current.kind === "term") {
            output.push(current.term);
            return;
        }
        if (current.kind === "and") {
            for (const child of current.children) {
                visit(child);
            }
        }
    };
    visit(plan);
    return output;
}
export function matchesIndexPlan(document, plan) {
    switch (plan.kind) {
        case "all":
            return true;
        case "term":
            return matchesTerm(document, plan.term);
        case "and":
            return plan.children.every((child) => matchesIndexPlan(document, child));
        case "or":
            return plan.children.some((child) => matchesIndexPlan(document, child));
    }
}
export const indexPlanMatchesDocument = matchesIndexPlan;
export const matchesPlan = (plan, document) => matchesIndexPlan(document, plan);
function matchesTerm(document, term) {
    return visitPath(document, term.path, 0, (value) => {
        if (term.test === "exists") {
            return true;
        }
        return jsonEqual(value, term.value);
    });
}
function visitPath(value, path, index, test, depth = 0) {
    if (depth > 10_000) {
        return false;
    }
    if (index >= path.length) {
        return test(value);
    }
    const segment = path[index];
    switch (segment.kind) {
        case "key":
            return (value !== null &&
                typeof value === "object" &&
                !Array.isArray(value) &&
                Object.prototype.hasOwnProperty.call(value, segment.key) &&
                visitPath(value[segment.key], path, index + 1, test, depth + 1));
        case "anyKey":
            if (value === null ||
                typeof value !== "object" ||
                Array.isArray(value)) {
                return false;
            }
            for (const child of Object.values(value)) {
                if (visitPath(child, path, index + 1, test, depth + 1)) {
                    return true;
                }
            }
            return false;
        case "anyIndex":
            if (!Array.isArray(value)) {
                return false;
            }
            for (const child of value) {
                if (visitPath(child, path, index + 1, test, depth + 1)) {
                    return true;
                }
            }
            return false;
        case "index":
            return (Array.isArray(value) &&
                segment.index >= 0 &&
                segment.index < value.length &&
                visitPath(value[segment.index], path, index + 1, test, depth + 1));
        case "indexRange":
            return visitArraySelection(value, {
                kind: "arraySelection",
                wildcard: false,
                selectors: [
                    {
                        kind: "range",
                        from: { kind: "integer", value: segment.from },
                        to: { kind: "integer", value: segment.to },
                    },
                ],
            }, path, index, test, depth);
        case "arraySelection":
            return visitArraySelection(value, segment, path, index, test, depth);
        case "laxUnwrap":
            if (visitPath(value, path, index + 1, test, depth + 1)) {
                return true;
            }
            if (Array.isArray(value)) {
                for (const child of value) {
                    if (visitPath(child, path, index, test, depth + 1)) {
                        return true;
                    }
                }
            }
            return false;
        case "recursive":
            return (visitPath(value, path, index + 1, test, depth + 1) ||
                visitRecursiveChildren(value, path, index, test, depth + 1));
    }
}
function visitRecursiveChildren(value, path, index, test, depth) {
    if (Array.isArray(value)) {
        for (const child of value) {
            if (visitPath(child, path, index + 1, test, depth + 1) ||
                visitRecursiveChildren(child, path, index, test, depth + 1)) {
                return true;
            }
        }
        return false;
    }
    if (value !== null && typeof value === "object") {
        for (const child of Object.values(value)) {
            if (visitPath(child, path, index + 1, test, depth + 1) ||
                visitRecursiveChildren(child, path, index, test, depth + 1)) {
                return true;
            }
        }
    }
    return false;
}
function visitArraySelection(value, segment, path, index, test, depth) {
    const array = Array.isArray(value) || segment.wrapScalar !== true
        ? value
        : [value];
    if (!Array.isArray(array)) {
        return false;
    }
    if (segment.wildcard) {
        for (const child of array) {
            if (visitPath(child, path, index + 1, test, depth + 1)) {
                return true;
            }
        }
        return false;
    }
    const selected = new Set();
    for (const selector of segment.selectors) {
        if (selector.kind === "index") {
            const selectedIndex = evaluateIndexExpression(selector.expression, array.length);
            if (selectedIndex !== null &&
                selectedIndex >= 0 &&
                selectedIndex < array.length) {
                selected.add(selectedIndex);
            }
            continue;
        }
        const from = evaluateIndexExpression(selector.from, array.length);
        const to = evaluateIndexExpression(selector.to, array.length);
        if (from === null || to === null || from > to) {
            continue;
        }
        for (let selectedIndex = from; selectedIndex <= to; selectedIndex += 1) {
            if (selectedIndex >= 0 && selectedIndex < array.length) {
                selected.add(selectedIndex);
            }
        }
    }
    for (const selectedIndex of selected) {
        if (visitPath(array[selectedIndex], path, index + 1, test, depth + 1)) {
            return true;
        }
    }
    return false;
}
function evaluateIndexExpression(expression, length) {
    let value;
    switch (expression.kind) {
        case "integer":
            value = expression.value;
            break;
        case "last":
            value = length - 1;
            break;
        case "unary": {
            const operand = evaluateIndexExpression(expression.operand, length);
            if (operand === null) {
                return null;
            }
            value = expression.operator === "+" ? operand : -operand;
            break;
        }
        case "binary": {
            const left = evaluateIndexExpression(expression.left, length);
            const right = evaluateIndexExpression(expression.right, length);
            if (left === null || right === null) {
                return null;
            }
            switch (expression.operator) {
                case "+":
                    value = left + right;
                    break;
                case "-":
                    value = left - right;
                    break;
                case "*":
                    value = left * right;
                    break;
                case "/":
                    if (right === 0) {
                        return null;
                    }
                    value = left / right;
                    break;
                case "%":
                    if (right === 0) {
                        return null;
                    }
                    value = left % right;
                    break;
            }
            break;
        }
    }
    return Number.isInteger(value) ? value : null;
}
export function toJsonbContainment(plan) {
    const converted = convertPlan(plan);
    return converted ?? { kind: "true" };
}
export const toContainmentQuery = toJsonbContainment;
function convertPlan(plan) {
    switch (plan.kind) {
        case "all":
            return { kind: "true" };
        case "term":
            return convertTerm(plan.term);
        case "and": {
            const children = [];
            for (const child of plan.children) {
                const converted = convertPlan(child);
                if (converted === null) {
                    continue;
                }
                if (converted.kind === "true") {
                    continue;
                }
                children.push(converted);
            }
            if (children.length === 0) {
                return { kind: "true" };
            }
            if (children.length === 1) {
                return children[0];
            }
            return { kind: "and", children };
        }
        case "or": {
            const children = [];
            for (const child of plan.children) {
                const converted = convertPlan(child);
                if (converted === null) {
                    return null;
                }
                if (converted.kind === "true") {
                    return { kind: "true" };
                }
                children.push(converted);
            }
            if (children.length === 0) {
                return { kind: "true" };
            }
            if (children.length === 1) {
                return children[0];
            }
            return { kind: "or", children };
        }
    }
}
function convertTerm(term) {
    if (term.test !== "equals") {
        return null;
    }
    let value = term.value;
    for (let index = term.path.length - 1; index >= 0; index -= 1) {
        const segment = term.path[index];
        if (segment.kind === "key") {
            value = Object.fromEntries([[segment.key, value]]);
            continue;
        }
        if (segment.kind === "anyIndex" ||
            (segment.kind === "arraySelection" &&
                segment.wildcard &&
                segment.wrapScalar !== true)) {
            value = [value];
            continue;
        }
        return null;
    }
    return { kind: "contains", value };
}
//# sourceMappingURL=index-plan.js.map