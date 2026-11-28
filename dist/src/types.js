export class HMTypeError extends Error {
    code;
    details;
    constructor(code, message, details = {}) {
        super(message);
        this.name = "HMTypeError";
        this.code = code;
        this.details = details;
    }
}
let nextTypeVarId = 0;
let nextRowVarId = 0;
export const emptyRow = { tag: "empty" };
export const intType = { tag: "const", name: "Int" };
export const stringType = { tag: "const", name: "String" };
export const boolType = { tag: "const", name: "Bool" };
export const unitType = { tag: "const", name: "Unit" };
export const IntType = intType;
export const StringType = stringType;
export const BoolType = boolType;
export const UnitType = unitType;
export function freshTypeVariable(weak = false) {
    const variable = { tag: "tvar", id: nextTypeVarId++ };
    if (weak)
        variable.weak = true;
    return variable;
}
export function freshRowVariable(lacks = []) {
    return {
        tag: "rvar",
        id: nextRowVarId++,
        lacks: new Set(lacks),
    };
}
export const tvar = freshTypeVariable;
export const freshTypeVar = freshTypeVariable;
export const newTypeVariable = freshTypeVariable;
export const rowVar = freshRowVariable;
export const freshRow = freshRowVariable;
export const newRowVariable = freshRowVariable;
export function constType(name) {
    switch (name) {
        case "Int":
            return intType;
        case "String":
            return stringType;
        case "Bool":
            return boolType;
        case "Unit":
            return unitType;
        default:
            return { tag: "const", name };
    }
}
export function fnType(arg, result) {
    return { tag: "fun", arg, result };
}
export const funType = fnType;
export const arrowType = fnType;
export const functionType = fnType;
export function refType(element) {
    return { tag: "ref", element };
}
export function listType(element) {
    return { tag: "list", element };
}
export function tupleType(items) {
    return { tag: "tuple", items };
}
export function recordTypeFromRow(row = emptyRow) {
    return { tag: "record", row };
}
function entriesOfFields(fields) {
    if (fields instanceof Map) {
        return [...fields.entries()];
    }
    if (Array.isArray(fields)) {
        return fields.map(([label, field]) => [label, field]);
    }
    return Object.entries(fields);
}
/**
 * Build a record type.  Labels must be distinct.  If a tail is supplied it is
 * an open row, and `extendRow` records the corresponding `lacks` constraints.
 */
export function recordType(fields = {}, tail = emptyRow) {
    let row = tail;
    for (const [label, field] of entriesOfFields(fields)) {
        row = extendRow(label, field, row);
    }
    return recordTypeFromRow(row);
}
export const recordTypeWithTail = recordType;
export function pruneType(type) {
    let current = type;
    while (current.tag === "tvar" && current.binding !== undefined) {
        current = current.binding;
    }
    return current;
}
export function pruneRow(row) {
    let current = row;
    while (current.tag === "rvar" && current.binding !== undefined) {
        current = current.binding;
    }
    return current;
}
export function occursInType(typeVarId, type) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            return current.id === typeVarId;
        case "const":
            return false;
        case "fun":
            return (occursInType(typeVarId, current.arg) ||
                occursInType(typeVarId, current.result));
        case "record":
            return occursInRowForType(typeVarId, current.row);
        case "ref":
        case "list":
            return occursInType(typeVarId, current.element);
        case "tuple":
            return current.items.some((item) => occursInType(typeVarId, item));
    }
}
function occursInRowForType(typeVarId, row) {
    const current = pruneRow(row);
    switch (current.tag) {
        case "empty":
        case "rvar":
            return false;
        case "extend":
            return (occursInType(typeVarId, current.field) ||
                occursInRowForType(typeVarId, current.tail));
    }
}
export function occursInRow(rowVarId, row) {
    const current = pruneRow(row);
    switch (current.tag) {
        case "empty":
            return false;
        case "rvar":
            return current.id === rowVarId;
        case "extend":
            return (occursInRowInType(rowVarId, current.field) ||
                occursInRow(rowVarId, current.tail));
    }
}
function occursInRowInType(rowVarId, type) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
        case "const":
            return false;
        case "fun":
            return (occursInRowInType(rowVarId, current.arg) ||
                occursInRowInType(rowVarId, current.result));
        case "record":
            return occursInRow(rowVarId, current.row);
        case "ref":
        case "list":
            return occursInRowInType(rowVarId, current.element);
        case "tuple":
            return current.items.some((item) => occursInRowInType(rowVarId, item));
    }
}
/**
 * Flatten a row into a label/type map and an empty or unbound row-variable
 * tail.  This makes field order irrelevant and catches duplicate labels.
 */
export function flattenRow(row) {
    const fields = new Map();
    let current = pruneRow(row);
    while (current.tag === "extend") {
        if (fields.has(current.label)) {
            throw new HMTypeError("DUPLICATE_FIELD", `duplicate record field '${current.label}'`, { label: current.label });
        }
        fields.set(current.label, current.field);
        current = pruneRow(current.tail);
    }
    return {
        fields,
        tail: current,
    };
}
export const rowFields = flattenRow;
export function requireRowLacks(row, labels, code = "LACKS_VIOLATION") {
    let current = pruneRow(row);
    const needed = [...labels];
    if (needed.length === 0)
        return;
    if (current.tag === "empty")
        return;
    if (current.tag === "rvar") {
        for (const label of needed)
            current.lacks.add(label);
        return;
    }
    const flat = flattenRow(current);
    for (const label of needed) {
        if (flat.fields.has(label)) {
            throw new HMTypeError(code, code === "DUPLICATE_FIELD"
                ? `duplicate record field '${label}'`
                : `row is constrained not to contain field '${label}'`, { label });
        }
    }
    requireRowLacks(flat.tail, needed, code);
}
/**
 * Extend a row with a field.  The tail must lack the label; if it is an
 * unbound row variable this is recorded as a residual constraint.
 */
export function extendRow(label, field, tail = emptyRow) {
    const prunedTail = pruneRow(tail);
    requireRowLacks(prunedTail, [label], "DUPLICATE_FIELD");
    return { tag: "extend", label, field, tail: prunedTail };
}
export const rowExtend = extendRow;
export const rowLacks = requireRowLacks;
export function rowFromFields(fields, tail = emptyRow) {
    let row = tail;
    for (const [label, field] of entriesOfFields(fields)) {
        row = extendRow(label, field, row);
    }
    return row;
}
function bindTypeVariable(variable, target) {
    const pruned = pruneType(target);
    if (pruned.tag === "tvar" && pruned.id === variable.id)
        return;
    if (occursInType(variable.id, pruned)) {
        throw new HMTypeError("OCCURS_CHECK", `infinite type: type variable #${variable.id} occurs in its own definition`, { typeVar: variable.id });
    }
    variable.binding = pruned;
    if (variable.weak === true)
        markWeakType(pruned);
}
function bindRowVariable(variable, target) {
    const pruned = pruneRow(target);
    if (pruned.tag === "rvar" && pruned.id === variable.id)
        return;
    if (occursInRow(variable.id, pruned)) {
        throw new HMTypeError("ROW_OCCURS_CHECK", `infinite row: row variable #${variable.id} occurs in its own tail`, { rowVar: variable.id });
    }
    const flat = flattenRow(pruned);
    for (const label of variable.lacks) {
        if (flat.fields.has(label)) {
            throw new HMTypeError("LACKS_VIOLATION", `row variable #${variable.id} lacks '${label}' but was unified with a row containing it`, { rowVar: variable.id, label });
        }
    }
    if (pruned.tag === "rvar") {
        for (const label of variable.lacks)
            pruned.lacks.add(label);
        if (variable.weak === true)
            pruned.weak = true;
        variable.binding = pruned;
        return;
    }
    const residual = [...variable.lacks].filter((label) => !flat.fields.has(label));
    requireRowLacks(flat.tail, residual);
    variable.binding = pruned;
    if (variable.weak === true)
        markWeakRow(pruned);
}
export function unifyRowVariables(left, right) {
    bindRowVariable(left, right);
}
function bindRowVariableToExtension(variable, fields, tail) {
    let row = tail;
    for (const [label, field] of fields) {
        row = extendRow(label, field, row);
    }
    bindRowVariable(variable, row);
}
function unifyRowTails(left, right) {
    if (left.tag === "empty" && right.tag === "empty")
        return;
    if (left.tag === "rvar" && right.tag === "rvar") {
        bindRowVariable(left, right);
        return;
    }
    if (left.tag === "rvar") {
        bindRowVariable(left, emptyRow);
        return;
    }
    if (right.tag === "rvar") {
        bindRowVariable(right, emptyRow);
    }
}
export function unifyRows(left, right) {
    const prunedLeft = pruneRow(left);
    const prunedRight = pruneRow(right);
    if (prunedLeft === prunedRight)
        return;
    if (prunedLeft.tag === "rvar" && prunedRight.tag === "rvar") {
        bindRowVariable(prunedLeft, prunedRight);
        return;
    }
    if (prunedLeft.tag === "rvar" && prunedRight.tag === "empty") {
        bindRowVariable(prunedLeft, emptyRow);
        return;
    }
    if (prunedLeft.tag === "empty" && prunedRight.tag === "rvar") {
        bindRowVariable(prunedRight, emptyRow);
        return;
    }
    if (prunedLeft.tag === "empty" && prunedRight.tag === "empty")
        return;
    const leftFlat = flattenRow(prunedLeft);
    const rightFlat = flattenRow(prunedRight);
    for (const [label, field] of leftFlat.fields) {
        const other = rightFlat.fields.get(label);
        if (other !== undefined)
            unifyTypes(field, other);
    }
    const onlyLeft = new Map();
    for (const [label, field] of leftFlat.fields) {
        if (!rightFlat.fields.has(label))
            onlyLeft.set(label, field);
    }
    const onlyRight = new Map();
    for (const [label, field] of rightFlat.fields) {
        if (!leftFlat.fields.has(label))
            onlyRight.set(label, field);
    }
    if (onlyLeft.size === 0 && onlyRight.size === 0) {
        unifyRowTails(leftFlat.tail, rightFlat.tail);
        return;
    }
    if (onlyLeft.size > 0 && onlyRight.size === 0) {
        if (rightFlat.tail.tag !== "rvar") {
            throw new HMTypeError("TYPE_MISMATCH", "record rows have different fields and the right row is closed", { leftOnly: [...onlyLeft.keys()] });
        }
        bindRowVariableToExtension(rightFlat.tail, onlyLeft, leftFlat.tail);
        return;
    }
    if (onlyRight.size > 0 && onlyLeft.size === 0) {
        if (leftFlat.tail.tag !== "rvar") {
            throw new HMTypeError("TYPE_MISMATCH", "record rows have different fields and the left row is closed", { rightOnly: [...onlyRight.keys()] });
        }
        bindRowVariableToExtension(leftFlat.tail, onlyRight, rightFlat.tail);
        return;
    }
    if (leftFlat.tail.tag !== "rvar" || rightFlat.tail.tag !== "rvar") {
        throw new HMTypeError("TYPE_MISMATCH", "record rows have different fields and no common extensible tail", {
            leftOnly: [...onlyLeft.keys()],
            rightOnly: [...onlyRight.keys()],
        });
    }
    if (leftFlat.tail.id === rightFlat.tail.id) {
        throw new HMTypeError("ROW_OCCURS_CHECK", "the same row variable would have to contain two different sets of fields", { rowVar: leftFlat.tail.id });
    }
    // Both rows contain labels absent from the other.  Introduce one fresh
    // common tail; each original tail absorbs the other side's extra fields.
    const sharedTail = freshRowVariable();
    bindRowVariableToExtension(rightFlat.tail, onlyLeft, sharedTail);
    bindRowVariableToExtension(leftFlat.tail, onlyRight, sharedTail);
}
export function unifyTypes(left, right) {
    const a = pruneType(left);
    const b = pruneType(right);
    if (a === b)
        return;
    if (a.tag === "tvar" && b.tag === "tvar" && a.id === b.id)
        return;
    if (a.tag === "tvar") {
        bindTypeVariable(a, b);
        return;
    }
    if (b.tag === "tvar") {
        bindTypeVariable(b, a);
        return;
    }
    if (a.tag === "const" && b.tag === "const") {
        if (a.name === b.name)
            return;
        throw new HMTypeError("TYPE_MISMATCH", `cannot unify ${a.name} with ${b.name}`, { left: a.name, right: b.name });
    }
    if (a.tag === "fun" && b.tag === "fun") {
        unifyTypes(a.arg, b.arg);
        unifyTypes(a.result, b.result);
        return;
    }
    if (a.tag === "record" && b.tag === "record") {
        unifyRows(a.row, b.row);
        return;
    }
    if (a.tag === "ref" && b.tag === "ref") {
        unifyTypes(a.element, b.element);
        return;
    }
    if (a.tag === "list" && b.tag === "list") {
        unifyTypes(a.element, b.element);
        return;
    }
    if (a.tag === "tuple" && b.tag === "tuple") {
        if (a.items.length !== b.items.length) {
            throw new HMTypeError("TYPE_MISMATCH", `tuple arity mismatch: ${a.items.length} vs ${b.items.length}`);
        }
        for (let index = 0; index < a.items.length; index += 1) {
            unifyTypes(a.items[index], b.items[index]);
        }
        return;
    }
    throw new HMTypeError("TYPE_MISMATCH", `cannot unify ${describeType(a)} with ${describeType(b)}`, { left: a.tag, right: b.tag });
}
export const unify = unifyTypes;
export const unifyType = unifyTypes;
export const unifyRow = unifyRows;
export const occursCheckType = occursInType;
export const occursCheckRow = occursInRow;
function describeType(type) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            return `type variable #${current.id}`;
        case "const":
            return current.name;
        case "fun":
            return "function type";
        case "record":
            return "record type";
        case "ref":
            return "ref type";
        case "list":
            return "list type";
        case "tuple":
            return "tuple type";
    }
}
export function freeVariablesOfType(type) {
    const free = {
        types: new Map(),
        rows: new Map(),
    };
    collectFreeType(type, free);
    return free;
}
function collectFreeType(type, free) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            free.types.set(current.id, current);
            return;
        case "const":
            return;
        case "fun":
            collectFreeType(current.arg, free);
            collectFreeType(current.result, free);
            return;
        case "record":
            collectFreeRow(current.row, free);
            return;
        case "ref":
        case "list":
            collectFreeType(current.element, free);
            return;
        case "tuple":
            for (const item of current.items)
                collectFreeType(item, free);
            return;
    }
}
function collectFreeRow(row, free) {
    const current = pruneRow(row);
    switch (current.tag) {
        case "empty":
            return;
        case "rvar":
            free.rows.set(current.id, current);
            return;
        case "extend":
            collectFreeType(current.field, free);
            collectFreeRow(current.tail, free);
            return;
    }
}
export function freeTypeVariableIds(type) {
    return new Set(freeVariablesOfType(type).types.keys());
}
export function freeRowVariableIds(type) {
    return new Set(freeVariablesOfType(type).rows.keys());
}
export function freeVariablesOfScheme(scheme) {
    const free = freeVariablesOfType(scheme.type);
    for (const id of scheme.typeVars)
        free.types.delete(id);
    for (const id of scheme.rowVars.keys())
        free.rows.delete(id);
    return free;
}
export function freeVariablesOfEnvironment(environment) {
    const free = {
        types: new Map(),
        rows: new Map(),
    };
    for (const scheme of environment.values()) {
        const schemeFree = freeVariablesOfScheme(scheme);
        for (const [id, variable] of schemeFree.types)
            free.types.set(id, variable);
        for (const [id, variable] of schemeFree.rows)
            free.rows.set(id, variable);
    }
    return free;
}
/**
 * Generalize a monotype into a scheme.  Variables that are weak, or that occur
 * free in the environment, are deliberately not quantified.
 */
export function generalize(type, environment = new Map(), options = true) {
    const quantify = typeof options === "boolean" ? options : (options.quantify ?? true);
    const respectWeak = typeof options === "boolean" ? true : (options.respectWeak ?? true);
    if (!quantify)
        return monoScheme(type);
    const free = freeVariablesOfType(type);
    const environmentFree = freeVariablesOfEnvironment(environment);
    const typeVars = new Set();
    const rowVars = new Map();
    for (const [id, variable] of free.types) {
        if (environmentFree.types.has(id))
            continue;
        if (respectWeak && variable.weak === true)
            continue;
        typeVars.add(id);
    }
    for (const [id, variable] of free.rows) {
        if (environmentFree.rows.has(id))
            continue;
        if (respectWeak && variable.weak === true)
            continue;
        rowVars.set(id, new Set(variable.lacks));
    }
    return {
        tag: "scheme",
        type,
        typeVars,
        rowVars,
    };
}
export function monoScheme(type) {
    return {
        tag: "scheme",
        type,
        typeVars: new Set(),
        rowVars: new Map(),
    };
}
export const mono = monoScheme;
export const monomorphic = monoScheme;
export function polymorphicScheme(type, environment = new Map()) {
    return generalize(type, environment, true);
}
export const scheme = polymorphicScheme;
export const quantify = polymorphicScheme;
export function instantiate(schemeValue) {
    if (schemeValue.typeVars.size === 0 && schemeValue.rowVars.size === 0) {
        return schemeValue.type;
    }
    const typeMap = new Map();
    const rowMap = new Map();
    const copyType = (type) => {
        const current = pruneType(type);
        switch (current.tag) {
            case "tvar": {
                if (!schemeValue.typeVars.has(current.id))
                    return current;
                let replacement = typeMap.get(current.id);
                if (replacement === undefined) {
                    replacement = freshTypeVariable();
                    typeMap.set(current.id, replacement);
                }
                return replacement;
            }
            case "const":
                return current;
            case "fun":
                return fnType(copyType(current.arg), copyType(current.result));
            case "record":
                return recordTypeFromRow(copyRow(current.row));
            case "ref":
                return refType(copyType(current.element));
            case "list":
                return listType(copyType(current.element));
            case "tuple":
                return tupleType(current.items.map(copyType));
        }
    };
    const copyRow = (row) => {
        const current = pruneRow(row);
        switch (current.tag) {
            case "empty":
                return emptyRow;
            case "rvar": {
                if (!schemeValue.rowVars.has(current.id))
                    return current;
                let replacement = rowMap.get(current.id);
                if (replacement === undefined) {
                    replacement = freshRowVariable(schemeValue.rowVars.get(current.id) ?? []);
                    rowMap.set(current.id, replacement);
                }
                return replacement;
            }
            case "extend":
                return {
                    tag: "extend",
                    label: current.label,
                    field: copyType(current.field),
                    tail: copyRow(current.tail),
                };
        }
    };
    return copyType(schemeValue.type);
}
export const instantiateScheme = instantiate;
export const instantiateTypeScheme = instantiate;
export function isTypeScheme(value) {
    return (typeof value === "object" &&
        value !== null &&
        value.tag === "scheme");
}
export function isType(value) {
    if (typeof value !== "object" || value === null)
        return false;
    const tag = value.tag;
    return (tag === "tvar" ||
        tag === "const" ||
        tag === "fun" ||
        tag === "record" ||
        tag === "ref" ||
        tag === "list" ||
        tag === "tuple");
}
export function isRow(value) {
    if (typeof value !== "object" || value === null)
        return false;
    const tag = value.tag;
    return tag === "empty" || tag === "extend" || tag === "rvar";
}
export function markWeakType(type) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            current.weak = true;
            return;
        case "const":
            return;
        case "fun":
            markWeakType(current.arg);
            markWeakType(current.result);
            return;
        case "record":
            markWeakRow(current.row);
            return;
        case "ref":
        case "list":
            markWeakType(current.element);
            return;
        case "tuple":
            for (const item of current.items)
                markWeakType(item);
            return;
    }
}
export function markWeakRow(row) {
    const current = pruneRow(row);
    switch (current.tag) {
        case "empty":
            return;
        case "rvar":
            current.weak = true;
            return;
        case "extend":
            markWeakType(current.field);
            markWeakRow(current.tail);
            return;
    }
}
export function schemeRowConstraints(schemeValue) {
    const result = {};
    for (const [id, labels] of schemeValue.rowVars) {
        result[String(id)] = [...labels].sort();
    }
    return result;
}
