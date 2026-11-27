import type { Assignment, DataRow, InsertAction, MergeResult, MergeStatement, UniqueIndexInput, UpdateAction, DeleteAction, DoNothingAction } from "./types.js";
/**
 * Executes one SQL MERGE statement.
 *
 * Matching is performed against the target snapshot captured before any write.
 * Therefore rows inserted by an earlier WHEN NOT MATCHED branch can never be
 * observed by a later source row.
 *
 * The caller's target and source arrays/rows are not modified. If a
 * cardinality or unique-constraint error is raised, no partial result escapes.
 */
export declare function executeMerge(statement: MergeStatement<DataRow, DataRow> & {
    readonly target: readonly [] | {
        readonly rows: readonly never[];
    };
}): MergeResult<DataRow>;
export declare function executeMerge<TargetRow extends object = DataRow>(statement: MergeStatement<TargetRow, DataRow> & {
    readonly source: readonly [] | {
        readonly rows: readonly never[];
    };
}): MergeResult<TargetRow>;
export declare function executeMerge<TargetRow extends object = DataRow, SourceRow extends object = DataRow>(statement: MergeStatement<TargetRow, SourceRow>): MergeResult<TargetRow>;
export declare function mergeRows(statement: MergeStatement<DataRow, DataRow> & {
    readonly target: readonly [] | {
        readonly rows: readonly never[];
    };
}): DataRow[];
export declare function mergeRows<TargetRow extends object = DataRow>(statement: MergeStatement<TargetRow, DataRow> & {
    readonly source: readonly [] | {
        readonly rows: readonly never[];
    };
}): TargetRow[];
export declare const mergeInto: typeof executeMerge;
export declare const merge: typeof executeMerge;
export declare const runMerge: typeof executeMerge;
export declare const applyMerge: typeof executeMerge;
export declare const executeSqlMerge: typeof executeMerge;
export declare function update<Row extends object = DataRow>(set: Assignment<NoInfer<Row>>): UpdateAction<Row>;
export declare function deleteRow(): DeleteAction;
export declare function insert<Row extends object = DataRow>(values?: Assignment<NoInfer<Row>>): InsertAction<Row>;
export declare function doNothing(): DoNothingAction;
export declare const noop: typeof doNothing;
export interface MergeTableOptions<Row extends object = DataRow> {
    rows?: readonly Row[];
    uniqueIndexes?: readonly UniqueIndexInput<Row>[];
    uniqueConstraints?: readonly UniqueIndexInput<Row>[];
}
export type MergeOperation<TargetRow extends object = DataRow, SourceRow extends object = DataRow> = Omit<MergeStatement<TargetRow, SourceRow>, "target">;
/**
 * Small stateful wrapper for callers that want a table object. State is only
 * replaced after a successful MERGE, so failed statements leave the table
 * untouched.
 */
export declare class MergeTable<Row extends object = DataRow> {
    private _rows;
    private readonly _indexes;
    constructor(options?: MergeTableOptions<Row> | readonly Row[]);
    get rows(): readonly Row[];
    get uniqueIndexes(): readonly UniqueIndexInput<Row>[];
    merge<SourceRow extends object = DataRow>(statement: MergeOperation<Row, SourceRow>): MergeResult<Row>;
    applyMerge<SourceRow extends object = DataRow>(statement: MergeOperation<Row, SourceRow>): MergeResult<Row>;
}
