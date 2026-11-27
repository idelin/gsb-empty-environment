export type MergeErrorCode = "CARDINALITY_VIOLATION" | "UNIQUE_VIOLATION" | "INVALID_MERGE_STATEMENT";
export declare const CARDINALITY_VIOLATION: "CARDINALITY_VIOLATION";
export declare const UNIQUE_VIOLATION: "UNIQUE_VIOLATION";
export declare const INVALID_MERGE_STATEMENT: "INVALID_MERGE_STATEMENT";
export declare class MergeError extends Error {
    readonly code: MergeErrorCode;
    readonly details: Readonly<Record<string, unknown>>;
    constructor(code: MergeErrorCode, message: string, details?: Readonly<Record<string, unknown>>);
}
export interface CardinalityViolationDetails<TargetRow extends object = object, SourceRow extends object = object> {
    readonly targetIndex: number;
    readonly sourceIndex: number;
    readonly targetRow: TargetRow;
    readonly sourceRow: SourceRow;
    readonly action: "update" | "delete";
}
export declare class CardinalityViolationError<TargetRow extends object = object, SourceRow extends object = object> extends MergeError {
    readonly targetIndex: number;
    readonly sourceIndex: number;
    readonly targetRow: TargetRow;
    readonly sourceRow: SourceRow;
    readonly action: "update" | "delete";
    constructor(details: CardinalityViolationDetails<TargetRow, SourceRow>);
}
export interface UniqueViolationDetails<Row extends object = object> {
    readonly index: string;
    readonly key: readonly unknown[];
    readonly rowIndexes: readonly [number, number];
    readonly rows: readonly [Row, Row];
}
export declare class UniqueViolationError<Row extends object = object> extends MergeError {
    readonly index: string;
    readonly key: readonly unknown[];
    readonly rowIndexes: readonly [number, number];
    readonly rows: readonly [Row, Row];
    constructor(details: UniqueViolationDetails<Row>);
}
