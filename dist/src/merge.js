import { MergeError, CardinalityViolationError, } from "./errors.js";
import { normalizeUniqueIndexes, validateNormalizedUniqueConstraints, } from "./constraints.js";
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function cloneRow(row) {
    return { ...row };
}
function readRows(input, label) {
    const rows = Array.isArray(input)
        ? input
        : isRecord(input) && Array.isArray(input.rows)
            ? input.rows
            : null;
    if (!rows) {
        throw new MergeError("INVALID_MERGE_STATEMENT", `${label} must be an array of rows or an object with a rows array`, { input });
    }
    return rows.map((row, index) => {
        if (!isRecord(row)) {
            throw new MergeError("INVALID_MERGE_STATEMENT", `${label}[${index}] is not a plain row object`, { index, row });
        }
        return cloneRow(row);
    });
}
function toArray(value) {
    if (value === undefined) {
        return [];
    }
    return Array.isArray(value) ? value : [value];
}
function normalizeKind(raw) {
    if (typeof raw !== "string") {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch action type must be a string", { type: raw });
    }
    const kind = raw.toLowerCase().replace(/[\s_-]+/g, "");
    switch (kind) {
        case "update":
            return "update";
        case "delete":
            return "delete";
        case "insert":
            return "insert";
        case "donothing":
        case "nothing":
        case "skip":
            return "doNothing";
        default:
            throw new MergeError("INVALID_MERGE_STATEMENT", `Unknown MERGE branch action "${raw}"`, { type: raw });
    }
}
function inferredKind(raw, branch) {
    if (raw.type !== undefined) {
        return normalizeKind(raw.type);
    }
    if (raw.update !== undefined || raw.set !== undefined) {
        return "update";
    }
    if (raw.delete === true) {
        return "delete";
    }
    if (raw.insert !== undefined || raw.values !== undefined) {
        return "insert";
    }
    if (raw.doNothing === true || raw.do_nothing === true) {
        return "doNothing";
    }
    if (branch.update !== undefined) {
        return "update";
    }
    if (branch.delete === true) {
        return "delete";
    }
    if (branch.insert !== undefined) {
        return "insert";
    }
    if (branch.doNothing === true) {
        return "doNothing";
    }
    throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch does not define an action", { branch });
}
function firstDefined(...values) {
    for (const value of values) {
        if (value !== undefined) {
            return value;
        }
    }
    return undefined;
}
function normalizeAction(branch, phase) {
    const candidate = firstDefined(branch.action, branch.then);
    let raw;
    if (candidate !== undefined) {
        if (typeof candidate === "string") {
            raw = { type: candidate };
        }
        else if (isRecord(candidate)) {
            raw = candidate;
        }
        else {
            throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch action must be a string or object", { branch });
        }
    }
    else if (branch.update !== undefined) {
        raw = { type: "update", update: branch.update };
    }
    else if (branch.delete === true) {
        raw = { type: "delete" };
    }
    else if (branch.insert !== undefined) {
        raw = { type: "insert", insert: branch.insert };
    }
    else if (branch.doNothing === true) {
        raw = { type: "doNothing" };
    }
    else if (branch.type !== undefined
        || branch.set !== undefined
        || branch.values !== undefined) {
        raw = branch;
    }
    else {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch does not define an action", { branch });
    }
    const kind = inferredKind(raw, branch);
    if (kind === "update") {
        if (phase === "notMatchedByTarget") {
            throw new MergeError("INVALID_MERGE_STATEMENT", "WHEN NOT MATCHED BY TARGET cannot perform UPDATE");
        }
        const set = firstDefined(raw.set, raw.values, raw.update, branch.set, branch.update);
        if (set === undefined) {
            throw new MergeError("INVALID_MERGE_STATEMENT", "UPDATE action requires a set assignment");
        }
        return { type: "update", set: set };
    }
    if (kind === "delete") {
        if (phase === "notMatchedByTarget") {
            throw new MergeError("INVALID_MERGE_STATEMENT", "WHEN NOT MATCHED BY TARGET cannot perform DELETE");
        }
        return { type: "delete" };
    }
    if (kind === "insert") {
        if (phase !== "notMatchedByTarget") {
            throw new MergeError("INVALID_MERGE_STATEMENT", "INSERT is only valid in WHEN NOT MATCHED [BY TARGET]");
        }
        const values = firstDefined(raw.values, raw.set, raw.insert, branch.values, branch.insert);
        return {
            type: "insert",
            values: values,
        };
    }
    return { type: "doNothing" };
}
function normalizeBranches(branches, phase) {
    return toArray(branches).map((branch) => {
        if (!isRecord(branch)) {
            throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch must be an object", { branch });
        }
        const condition = firstDefined(branch.condition, branch.and, branch.when);
        if (condition !== undefined
            && typeof condition !== "boolean"
            && typeof condition !== "function") {
            throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE branch condition must be a boolean or predicate function", { branch });
        }
        return {
            condition,
            action: normalizeAction(branch, phase),
        };
    });
}
function conditionValue(condition, phase, target, source, context) {
    if (condition === undefined) {
        return true;
    }
    if (typeof condition === "boolean") {
        return condition;
    }
    if (phase === "matched") {
        return condition(target, source, context) === true;
    }
    if (phase === "notMatchedByTarget") {
        // A one-argument predicate naturally receives the source row. A
        // two-or-more-argument predicate uses the uniform (target, source)
        // convention, with target explicitly null.
        if (condition.length >= 2) {
            return condition(null, source, context) === true;
        }
        return condition(source, context) === true;
    }
    if (condition.length >= 2) {
        return condition(target, null, context) === true;
    }
    return condition(target, context) === true;
}
function selectBranch(branches, phase, target, source, context) {
    for (const branch of branches) {
        if (conditionValue(branch.condition, phase, target, source, context)) {
            return branch;
        }
    }
    return undefined;
}
function resolveAssignment(assignment, phase, target, source, context) {
    if (assignment === undefined) {
        return source ? cloneRow(source) : {};
    }
    const invoke = (resolver) => {
        if (phase === "matched") {
            return resolver(target, source, context);
        }
        if (phase === "notMatchedByTarget") {
            if (resolver.length >= 2) {
                return resolver(null, source, context);
            }
            return resolver(source, context, context);
        }
        if (resolver.length >= 2) {
            return resolver(target, null, context);
        }
        return resolver(target, context, context);
    };
    let result;
    if (typeof assignment === "function") {
        result = invoke(assignment);
    }
    else if (isRecord(assignment)) {
        const resolved = {};
        for (const [key, value] of Object.entries(assignment)) {
            resolved[key] = typeof value === "function"
                ? invoke(value)
                : value;
        }
        result = resolved;
    }
    else {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE assignment must be an object or resolver function", { assignment });
    }
    if (!isRecord(result)) {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE assignment resolver must return a row object", { result });
    }
    return result;
}
function activeSlots(state) {
    return state.slots.filter((slot) => !slot.deleted);
}
function checkImmediate(state) {
    if (state.immediateIndexes.length === 0) {
        return;
    }
    const active = activeSlots(state);
    validateNormalizedUniqueConstraints(active.map((slot) => slot.row), state.immediateIndexes, active.map((slot) => slot.id));
}
function cardinalityViolation(slot, source, sourceIndex, action) {
    throw new CardinalityViolationError({
        targetIndex: slot.id,
        sourceIndex,
        targetRow: cloneRow(slot.row),
        sourceRow: cloneRow(source),
        action,
    });
}
function executeMatchedAction(state, slot, source, sourceIndex, action, updatedRows, deletedRows, stats) {
    const context = {
        phase: "matched",
        target: slot.row,
        source,
        targetIndex: slot.id,
        sourceIndex,
    };
    if (action.type === "doNothing") {
        stats.doNothing += 1;
        return;
    }
    if (action.type === "insert") {
        throw new MergeError("INVALID_MERGE_STATEMENT", "INSERT cannot be used in a WHEN MATCHED branch");
    }
    if (state.modifiedTargets.has(slot.id)) {
        cardinalityViolation(slot, source, sourceIndex, action.type);
    }
    if (action.type === "update") {
        const patch = resolveAssignment(action.set, "matched", slot.row, source, context);
        slot.row = { ...slot.row, ...patch };
        state.modifiedTargets.add(slot.id);
        stats.updated += 1;
        updatedRows.push(cloneRow(slot.row));
        checkImmediate(state);
        return;
    }
    if (action.type === "delete") {
        const deleted = cloneRow(slot.row);
        slot.deleted = true;
        state.modifiedTargets.add(slot.id);
        stats.deleted += 1;
        deletedRows.push(deleted);
        checkImmediate(state);
        return;
    }
    throw new MergeError("INVALID_MERGE_STATEMENT", `Unsupported matched action "${action.type}"`);
}
function executeSourceInsert(state, source, sourceIndex, action, insertedRows, stats) {
    if (action.type === "doNothing") {
        stats.doNothing += 1;
        return;
    }
    if (action.type !== "insert") {
        throw new MergeError("INVALID_MERGE_STATEMENT", "Only INSERT or DO NOTHING is valid for an unmatched source row");
    }
    const context = {
        phase: "notMatchedByTarget",
        target: null,
        source,
        sourceIndex,
    };
    const values = resolveAssignment(action.values, "notMatchedByTarget", null, source, context);
    const row = { ...values };
    state.slots.push({
        id: state.nextId,
        row,
        deleted: false,
    });
    state.nextId += 1;
    stats.inserted += 1;
    insertedRows.push(cloneRow(row));
    checkImmediate(state);
}
function executeTargetAction(state, slot, action, updatedRows, deletedRows, stats) {
    const context = {
        phase: "notMatchedBySource",
        target: slot.row,
        source: null,
        targetIndex: slot.id,
    };
    if (action.type === "doNothing") {
        stats.doNothing += 1;
        return;
    }
    if (action.type === "update") {
        const patch = resolveAssignment(action.set, "notMatchedBySource", slot.row, null, context);
        slot.row = { ...slot.row, ...patch };
        stats.updated += 1;
        updatedRows.push(cloneRow(slot.row));
        checkImmediate(state);
        return;
    }
    if (action.type === "delete") {
        const deleted = cloneRow(slot.row);
        slot.deleted = true;
        stats.deleted += 1;
        deletedRows.push(deleted);
        checkImmediate(state);
        return;
    }
    throw new MergeError("INVALID_MERGE_STATEMENT", "INSERT cannot be used in WHEN NOT MATCHED BY SOURCE");
}
function collectIndexes(statement) {
    const collect = (value) => {
        if (value === undefined) {
            return [];
        }
        return Array.isArray(value)
            ? [...value]
            : [value];
    };
    return [
        ...collect(statement.uniqueIndexes),
        ...collect(statement.uniqueConstraints),
        ...collect(statement.constraints),
    ];
}
export function executeMerge(statement) {
    if (!isRecord(statement)) {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE statement must be an object", { statement });
    }
    if (typeof statement.on !== "function") {
        throw new MergeError("INVALID_MERGE_STATEMENT", "MERGE statement requires an on(target, source) predicate");
    }
    const targetRows = readRows(statement.target, "target");
    const sourceRows = readRows(statement.source, "source");
    const matchedBranches = normalizeBranches(statement.whenMatched, "matched");
    const notMatchedByTargetBranches = normalizeBranches([
        ...toArray(statement.whenNotMatchedByTarget),
        ...toArray(statement.whenNotMatched),
    ], "notMatchedByTarget");
    const notMatchedBySourceBranches = normalizeBranches(statement.whenNotMatchedBySource, "notMatchedBySource");
    const indexes = normalizeUniqueIndexes(collectIndexes(statement));
    const snapshot = targetRows.map((row, index) => ({
        id: index,
        row,
        deleted: false,
    }));
    const state = {
        slots: [...snapshot],
        snapshot,
        modifiedTargets: new Set(),
        indexes,
        immediateIndexes: indexes.filter((index) => index.timing === "immediate"),
        nextId: snapshot.length,
    };
    const stats = {
        targetRowsBefore: snapshot.length,
        targetRowsAfter: snapshot.length,
        sourceRows: sourceRows.length,
        matchedPairs: 0,
        matchedSourceRows: 0,
        unmatchedSourceRows: 0,
        unmatchedTargetRows: 0,
        updated: 0,
        deleted: 0,
        inserted: 0,
        doNothing: 0,
    };
    const insertedRows = [];
    const updatedRows = [];
    const deletedRows = [];
    const matchesBySource = sourceRows.map((source, sourceIndex) => {
        const matches = [];
        for (const target of snapshot) {
            const context = {
                phase: "on",
                target: target.row,
                source,
                targetIndex: target.id,
                sourceIndex,
            };
            if (statement.on(target.row, source, context) === true) {
                matches.push(target);
            }
        }
        return matches;
    });
    const matchedTargetIds = new Set();
    for (const matches of matchesBySource) {
        for (const target of matches) {
            matchedTargetIds.add(target.id);
        }
    }
    stats.matchedPairs = matchesBySource.reduce((total, matches) => total + matches.length, 0);
    stats.unmatchedTargetRows = snapshot.length - matchedTargetIds.size;
    for (let sourceIndex = 0; sourceIndex < sourceRows.length; sourceIndex += 1) {
        const source = sourceRows[sourceIndex];
        const matches = matchesBySource[sourceIndex];
        if (matches.length === 0) {
            stats.unmatchedSourceRows += 1;
            const branch = selectBranch(notMatchedByTargetBranches, "notMatchedByTarget", null, source, {
                phase: "notMatchedByTarget",
                target: null,
                source,
                sourceIndex,
            });
            if (branch) {
                executeSourceInsert(state, source, sourceIndex, branch.action, insertedRows, stats);
            }
            continue;
        }
        stats.matchedSourceRows += 1;
        for (const target of matches) {
            const branch = selectBranch(matchedBranches, "matched", target.row, source, {
                phase: "matched",
                target: target.row,
                source,
                targetIndex: target.id,
                sourceIndex,
            });
            if (branch) {
                executeMatchedAction(state, target, source, sourceIndex, branch.action, updatedRows, deletedRows, stats);
            }
        }
    }
    for (const target of snapshot) {
        if (matchedTargetIds.has(target.id) || target.deleted) {
            continue;
        }
        const branch = selectBranch(notMatchedBySourceBranches, "notMatchedBySource", target.row, null, {
            phase: "notMatchedBySource",
            target: target.row,
            source: null,
            targetIndex: target.id,
        });
        if (branch) {
            executeTargetAction(state, target, branch.action, updatedRows, deletedRows, stats);
        }
    }
    const active = activeSlots(state);
    validateNormalizedUniqueConstraints(active.map((slot) => slot.row), state.indexes, active.map((slot) => slot.id));
    const rows = active.map((slot) => slot.row);
    stats.targetRowsAfter = rows.length;
    return {
        rows,
        target: rows,
        inserted: insertedRows,
        updated: updatedRows,
        deleted: deletedRows,
        insertedRows,
        updatedRows,
        deletedRows,
        stats: stats,
    };
}
export function mergeRows(statement) {
    return executeMerge(statement).rows;
}
export const mergeInto = executeMerge;
export const merge = executeMerge;
export const runMerge = executeMerge;
export const applyMerge = executeMerge;
export const executeSqlMerge = executeMerge;
export function update(set) {
    return { type: "update", set };
}
export function deleteRow() {
    return { type: "delete" };
}
export function insert(values) {
    return values === undefined
        ? { type: "insert" }
        : { type: "insert", values };
}
export function doNothing() {
    return { type: "doNothing" };
}
export const noop = doNothing;
/**
 * Small stateful wrapper for callers that want a table object. State is only
 * replaced after a successful MERGE, so failed statements leave the table
 * untouched.
 */
export class MergeTable {
    _rows;
    _indexes;
    constructor(options = []) {
        const config = Array.isArray(options)
            ? { rows: options }
            : options;
        this._rows = (config.rows ?? []).map((row) => cloneRow(row));
        this._indexes = [
            ...(config.uniqueIndexes ?? []),
            ...(config.uniqueConstraints ?? []),
        ];
    }
    get rows() {
        return this._rows;
    }
    get uniqueIndexes() {
        return this._indexes;
    }
    merge(statement) {
        const result = executeMerge({
            ...statement,
            target: this._rows,
            uniqueIndexes: [
                ...this._indexes,
                ...(statement.uniqueIndexes ?? []),
            ],
        });
        this._rows = [...result.rows];
        return result;
    }
    applyMerge(statement) {
        return this.merge(statement);
    }
}
//# sourceMappingURL=merge.js.map