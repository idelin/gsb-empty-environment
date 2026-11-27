export const CARDINALITY_VIOLATION = "CARDINALITY_VIOLATION";
export const UNIQUE_VIOLATION = "UNIQUE_VIOLATION";
export const INVALID_MERGE_STATEMENT = "INVALID_MERGE_STATEMENT";
export class MergeError extends Error {
    code;
    details;
    constructor(code, message, details = {}) {
        super(message);
        this.name = new.target.name;
        this.code = code;
        this.details = details;
    }
}
export class CardinalityViolationError extends MergeError {
    targetIndex;
    sourceIndex;
    targetRow;
    sourceRow;
    action;
    constructor(details) {
        super("CARDINALITY_VIOLATION", `MERGE cardinality violation (CARDINALITY_VIOLATION): target row ${details.targetIndex} was already modified by an earlier source row and cannot be modified again by source row ${details.sourceIndex}`, details);
        this.targetIndex = details.targetIndex;
        this.sourceIndex = details.sourceIndex;
        this.targetRow = details.targetRow;
        this.sourceRow = details.sourceRow;
        this.action = details.action;
    }
}
export class UniqueViolationError extends MergeError {
    index;
    key;
    rowIndexes;
    rows;
    constructor(details) {
        super("UNIQUE_VIOLATION", `Unique constraint "${details.index}" violated (UNIQUE_VIOLATION) by target rows ${details.rowIndexes[0]} and ${details.rowIndexes[1]}`, details);
        this.index = details.index;
        this.key = details.key;
        this.rowIndexes = details.rowIndexes;
        this.rows = details.rows;
    }
}
//# sourceMappingURL=errors.js.map