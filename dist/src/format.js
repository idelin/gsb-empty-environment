import { flattenRow, isTypeScheme, pruneType, } from "./types.js";
function nextTypeName(state) {
    const letters = "abcdefghijklmnopqrstuvwxyz";
    while (true) {
        const index = state.typeCounter++;
        const suffix = index < letters.length
            ? letters[index]
            : `${letters[index % letters.length]}${Math.floor(index / letters.length)}`;
        const candidate = `'${suffix}`;
        if (!state.used.has(candidate)) {
            state.used.add(candidate);
            return candidate;
        }
    }
}
function nextRowName(state) {
    const letters = "rstuvwxyz";
    while (true) {
        const index = state.rowCounter++;
        const suffix = index < letters.length
            ? letters[index]
            : `r${index - letters.length + 1}`;
        const candidate = `'${suffix}`;
        if (!state.used.has(candidate)) {
            state.used.add(candidate);
            return candidate;
        }
    }
}
function ensureTypeName(variable, state) {
    let name = state.typeNames.get(variable.id);
    if (name === undefined) {
        name = nextTypeName(state);
        state.typeNames.set(variable.id, name);
        state.order.push({ kind: "type", id: variable.id });
    }
    return name;
}
function ensureRowName(variable, state) {
    let name = state.rowNames.get(variable.id);
    if (name === undefined) {
        name = nextRowName(state);
        state.rowNames.set(variable.id, name);
        state.order.push({ kind: "row", id: variable.id });
    }
    return name;
}
function assignNames(type, state, sortFields) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            ensureTypeName(current, state);
            return;
        case "const":
            return;
        case "fun":
            assignNames(current.arg, state, sortFields);
            assignNames(current.result, state, sortFields);
            return;
        case "record":
            assignRowNames(current.row, state, sortFields);
            return;
        case "ref":
        case "list":
            assignNames(current.element, state, sortFields);
            return;
        case "tuple":
            for (const item of current.items)
                assignNames(item, state, sortFields);
            return;
    }
}
function assignRowNames(row, state, sortFields) {
    const flat = flattenRow(row);
    const entries = [...flat.fields.entries()];
    if (sortFields)
        entries.sort(([left], [right]) => left.localeCompare(right));
    for (const [, field] of entries)
        assignNames(field, state, sortFields);
    if (flat.tail.tag === "rvar")
        ensureRowName(flat.tail, state);
}
function renderType(type, state, sortFields) {
    const current = pruneType(type);
    switch (current.tag) {
        case "tvar":
            return ensureTypeName(current, state);
        case "const":
            return current.name;
        case "fun": {
            const argument = renderType(current.arg, state, sortFields);
            const renderedArgument = pruneType(current.arg).tag === "fun" ? `(${argument})` : argument;
            return `${renderedArgument} -> ${renderType(current.result, state, sortFields)}`;
        }
        case "record":
            return renderRow(current.row, state, sortFields);
        case "ref": {
            const element = renderType(current.element, state, sortFields);
            const wrapped = pruneType(current.element).tag === "fun" ? `(${element})` : element;
            return `Ref<${wrapped}>`;
        }
        case "list": {
            const element = renderType(current.element, state, sortFields);
            const wrapped = pruneType(current.element).tag === "fun" ? `(${element})` : element;
            return `List<${wrapped}>`;
        }
        case "tuple":
            return `(${current.items
                .map((item) => renderType(item, state, sortFields))
                .join(", ")})`;
    }
}
function renderRow(row, state, sortFields) {
    const flat = flattenRow(row);
    const entries = [...flat.fields.entries()];
    if (sortFields)
        entries.sort(([left], [right]) => left.localeCompare(right));
    const fields = entries
        .map(([label, field]) => `${label}: ${renderType(field, state, sortFields)}`)
        .join(", ");
    if (flat.tail.tag === "empty") {
        return fields.length === 0 ? "{}" : `{${fields}}`;
    }
    const tail = ensureRowName(flat.tail, state);
    return fields.length === 0 ? `{ | ${tail}}` : `{${fields} | ${tail}}`;
}
function formatWithNaming(value, options) {
    const sortFields = options.sortFields ?? true;
    const state = {
        typeNames: new Map(),
        rowNames: new Map(),
        order: [],
        used: new Set(),
        typeCounter: 0,
        rowCounter: 0,
    };
    const scheme = isTypeScheme(value) ? value : undefined;
    const type = scheme === undefined ? value : scheme.type;
    assignNames(type, state, sortFields);
    const rendered = renderType(type, state, sortFields);
    if (scheme === undefined)
        return rendered;
    const quantify = options.quantify ?? true;
    const quantifiedNames = [];
    if (quantify) {
        for (const entry of state.order) {
            if (entry.kind === "type" && scheme.typeVars.has(entry.id)) {
                const name = state.typeNames.get(entry.id);
                if (name !== undefined)
                    quantifiedNames.push(name);
            }
            if (entry.kind === "row" && scheme.rowVars.has(entry.id)) {
                const name = state.rowNames.get(entry.id);
                if (name !== undefined)
                    quantifiedNames.push(name);
            }
        }
    }
    const prefix = quantifiedNames.length === 0
        ? ""
        : `forall ${quantifiedNames.join(" ")}. `;
    if (options.showLacks !== true)
        return `${prefix}${rendered}`;
    const constraints = [];
    for (const entry of state.order) {
        if (entry.kind !== "row")
            continue;
        const labels = scheme.rowVars.get(entry.id);
        if (labels === undefined || labels.size === 0)
            continue;
        const name = state.rowNames.get(entry.id);
        if (name === undefined)
            continue;
        constraints.push(`lacks(${name}, {${[...labels].sort().join(", ")}})`);
    }
    return constraints.length === 0
        ? `${prefix}${rendered}`
        : `${prefix}${rendered} where ${constraints.join(", ")}`;
}
export function typeToString(value, options = {}) {
    return formatWithNaming(value, options);
}
export const formatType = typeToString;
export const formatScheme = typeToString;
export const showScheme = typeToString;
export const schemeToString = typeToString;
export const formatTypeString = typeToString;
export const showType = typeToString;
export const prettyType = typeToString;
export const typeText = typeToString;
export const pretty = typeToString;
export const format = typeToString;
export const show = typeToString;
