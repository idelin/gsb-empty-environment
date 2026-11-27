import { MergeError, UniqueViolationError, } from "./errors.js";
function isNullish(value) {
    return value === null || value === undefined;
}
function normalizeNulls(input) {
    if (input.nullsNotDistinct === true || input.nullsDistinct === false) {
        return "not distinct";
    }
    const raw = typeof input.nulls === "string"
        ? input.nulls.toLowerCase().replace(/[\s_-]+/g, " ")
        : "";
    if (raw.includes("not distinct") || raw.replace(/\s+/g, "").includes("notdistinct")) {
        return "not distinct";
    }
    return "distinct";
}
function normalizeTiming(input) {
    if (input.deferred === true) {
        return "deferred";
    }
    const requested = input.timing ?? input.mode;
    const raw = typeof requested === "string"
        ? requested.toLowerCase()
        : "";
    return raw.includes("defer") ? "deferred" : "immediate";
}
function normalizeColumns(input) {
    if (Array.isArray(input.columns)) {
        return input.columns.map((column) => String(column));
    }
    if (typeof input.columns === "string") {
        return [input.columns];
    }
    if (Array.isArray(input.fields)) {
        return input.fields.map((column) => String(column));
    }
    if (typeof input.column === "string") {
        return [input.column];
    }
    return [];
}
export function normalizeUniqueIndexes(inputs) {
    if (!inputs) {
        return [];
    }
    return inputs.map((input, ordinal) => {
        let source;
        if (typeof input === "string") {
            source = { columns: [input] };
        }
        else if (Array.isArray(input)) {
            source = { columns: input.map((column) => String(column)) };
        }
        else {
            source = input;
        }
        const columns = normalizeColumns(source);
        if (columns.length === 0 && typeof source.key !== "function") {
            throw new MergeError("INVALID_MERGE_STATEMENT", `Unique index ${ordinal} must define at least one column or a key resolver`, { index: ordinal, input: source });
        }
        return {
            name: source.name ?? (columns.length > 0
                ? `unique_${columns.join("_")}`
                : `unique_key_${ordinal}`),
            columns,
            key: source.key,
            timing: normalizeTiming(source),
            nulls: normalizeNulls(source),
        };
    });
}
function encodeString(value) {
    return `S${value.length}:${value}`;
}
function encodeNumber(value) {
    if (Number.isNaN(value)) {
        return "n:NaN";
    }
    if (value === Infinity) {
        return "n:+Infinity";
    }
    if (value === -Infinity) {
        return "n:-Infinity";
    }
    if (Object.is(value, -0)) {
        return "n:-0";
    }
    return `n:${value}`;
}
function encodeArray(value, seen) {
    if (seen.has(value)) {
        return "C";
    }
    seen.add(value);
    const body = value.map((item) => encodeValue(item, seen)).join("");
    seen.delete(value);
    return `A${value.length}[${body}]`;
}
function encodeObject(value, seen) {
    if (seen.has(value)) {
        return "C";
    }
    seen.add(value);
    const keys = Object.keys(value).sort();
    const body = keys
        .map((key) => `${encodeString(key)}${encodeValue(value[key], seen)}`)
        .join("");
    seen.delete(value);
    return `O${keys.length}{${body}}`;
}
function encodeValue(value, seen = new WeakSet()) {
    if (value === null || value === undefined) {
        return "N";
    }
    switch (typeof value) {
        case "string":
            return encodeString(value);
        case "number":
            return encodeNumber(value);
        case "bigint":
            return `i:${value.toString()}n`;
        case "boolean":
            return value ? "b:1" : "b:0";
        case "symbol":
            return `y:${String(value)}`;
        case "function":
            return `f:${String(value)}`;
        case "object":
            break;
        default:
            return `u:${String(value)}`;
    }
    if (value instanceof Date) {
        return `d:${value.toISOString()}`;
    }
    if (value instanceof RegExp) {
        return `r:${value.source}/${value.flags}`;
    }
    if (Array.isArray(value)) {
        return encodeArray(value, seen);
    }
    if (value instanceof Uint8Array) {
        return `x:${Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
    }
    return encodeObject(value, seen);
}
function extractKey(index, row) {
    const rawValues = typeof index.key === "function"
        ? (() => {
            const result = index.key(row);
            return Array.isArray(result) ? result : [result];
        })()
        : index.columns.map((column) => row[column]);
    // In SQL NULLS DISTINCT mode, a key containing any NULL is never equal to
    // another key, including another key with the same NULL position.
    if (index.nulls === "distinct" && rawValues.some(isNullish)) {
        return { values: rawValues, encoded: null };
    }
    return {
        values: rawValues.map((value) => value === undefined ? null : value),
        encoded: encodeArray(rawValues, new WeakSet()),
    };
}
function rowLabelsFor(rows, labels) {
    return labels ?? rows.map((_, index) => index);
}
/**
 * Finds the first duplicate in any of the supplied unique indexes without
 * throwing. The caller may supply stable row labels for diagnostics.
 */
export function findUniqueViolation(rows, indexes, rowLabels) {
    const normalized = normalizeUniqueIndexes(indexes);
    const labels = rowLabelsFor(rows, rowLabels);
    if (labels.length !== rows.length) {
        throw new RangeError("rowLabels must have the same length as rows");
    }
    for (const index of normalized) {
        const seen = new Map();
        for (let rowOffset = 0; rowOffset < rows.length; rowOffset += 1) {
            const row = rows[rowOffset];
            const extracted = extractKey(index, row);
            if (extracted.encoded === null) {
                continue;
            }
            const previous = seen.get(extracted.encoded);
            if (previous) {
                return {
                    index: index.name,
                    key: extracted.values,
                    rowIndexes: [previous.rowIndex, labels[rowOffset]],
                    rows: [previous.row, row],
                };
            }
            seen.set(extracted.encoded, {
                row,
                rowIndex: labels[rowOffset],
                key: extracted.values,
            });
        }
    }
    return null;
}
/**
 * Validates all unique indexes against the supplied rows. Rows containing a
 * NULL key component are exempt in NULLS DISTINCT mode.
 */
export function validateUniqueConstraints(rows, indexes, rowLabels) {
    const violation = findUniqueViolation(rows, indexes, rowLabels);
    if (violation) {
        throw new UniqueViolationError(violation);
    }
}
export const checkUniqueConstraints = validateUniqueConstraints;
export function validateNormalizedUniqueConstraints(rows, indexes, rowLabels) {
    const violation = findUniqueViolation(rows, indexes, rowLabels);
    if (violation) {
        throw new UniqueViolationError(violation);
    }
}
//# sourceMappingURL=constraints.js.map