import type { DataRow, NullsMode, UniqueIndexInput, UniqueTiming, UniqueViolation } from "./types.js";
export interface NormalizedUniqueIndex<Row extends object = DataRow> {
    readonly name: string;
    readonly columns: readonly string[];
    readonly key?: (row: Row) => unknown;
    readonly timing: UniqueTiming;
    readonly nulls: NullsMode;
}
export declare function normalizeUniqueIndexes<Row extends object = DataRow>(inputs: readonly UniqueIndexInput<Row>[] | undefined): NormalizedUniqueIndex<Row>[];
/**
 * Finds the first duplicate in any of the supplied unique indexes without
 * throwing. The caller may supply stable row labels for diagnostics.
 */
export declare function findUniqueViolation<Row extends object = DataRow>(rows: readonly Row[], indexes: readonly UniqueIndexInput<Row>[] | readonly NormalizedUniqueIndex<Row>[], rowLabels?: readonly number[]): UniqueViolation<Row> | null;
/**
 * Validates all unique indexes against the supplied rows. Rows containing a
 * NULL key component are exempt in NULLS DISTINCT mode.
 */
export declare function validateUniqueConstraints<Row extends object = DataRow>(rows: readonly Row[], indexes: readonly UniqueIndexInput<Row>[], rowLabels?: readonly number[]): void;
export declare const checkUniqueConstraints: typeof validateUniqueConstraints;
export declare function validateNormalizedUniqueConstraints<Row extends object>(rows: readonly Row[], indexes: readonly NormalizedUniqueIndex<Row>[], rowLabels?: readonly number[]): void;
