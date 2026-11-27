/**
 * Public types for the MERGE execution library.
 *
 * Rows are plain objects. The executor never mutates a row object supplied by
 * the caller; updates produce fresh shallow copies.
 */

export type DataRow = Record<string, any>;

export type MergePhase =
  | "on"
  | "matched"
  | "notMatchedByTarget"
  | "notMatchedBySource";

/**
 * Extra information passed as the last argument to predicates and assignment
 * resolvers. It is intentionally optional so simple callbacks can ignore it.
 */
export interface MergeContext<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> {
  readonly phase: MergePhase;
  readonly target: TargetRow | null;
  readonly source: SourceRow | null;
  readonly targetIndex?: number;
  readonly sourceIndex?: number;
}

/**
 * The ON predicate always receives the current target snapshot row first and
 * the source row second.
 */
export type MatchPredicate<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> = (
  target: TargetRow,
  source: SourceRow,
  context: MergeContext<TargetRow, SourceRow>,
) => boolean;

/**
 * Conditions are evaluated top-to-bottom. A branch is selected only when its
 * condition returns exactly `true`. Boolean constants are accepted as a
 * convenience.
 */
export type BranchCondition<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> =
  | boolean
  | ((
      first: any,
      second?: any,
      context?: MergeContext<TargetRow, SourceRow>,
    ) => boolean);

export type AssignmentResolver<Row extends object = DataRow> = (
  target: DataRow,
  source: DataRow,
  context: MergeContext<DataRow>,
) => Partial<Row> | Row;

export type AssignmentValueResolver = (
  target: DataRow,
  source: DataRow,
  context: MergeContext<DataRow>,
) => unknown;

export type AssignmentValue =
  | string
  | number
  | bigint
  | boolean
  | null
  | undefined
  | Date
  | readonly unknown[]
  | Record<string, unknown>
  | AssignmentValueResolver;

/**
 * An update/insert assignment can be:
 * - a plain object of literal values,
 * - a function returning a partial row, or
 * - an object whose individual column values may be resolver functions.
 */
export type Assignment<Row extends object = DataRow> =
  | Partial<Row>
  | AssignmentResolver<Row>
  | Record<string, AssignmentValue>;

export type MergeActionKind =
  | "update"
  | "delete"
  | "insert"
  | "doNothing";

export interface UpdateAction<Row extends object = DataRow> {
  type: "update";
  set: Assignment<Row>;
}

export interface DeleteAction {
  type: "delete";
}

export interface InsertAction<Row extends object = DataRow> {
  type: "insert";
  /**
   * When omitted, the source row is copied into the target.
   */
  values?: Assignment<Row>;
}

export interface DoNothingAction {
  type: "doNothing";
}

export type MergeAction<Row extends object = DataRow> =
  | UpdateAction<Row>
  | DeleteAction
  | InsertAction<Row>
  | DoNothingAction;

/**
 * Flexible action-object input. `{ type: "update", set }` is the canonical
 * form; `{ update }`, `{ delete: true }`, `{ insert }` and
 * `{ doNothing: true }` are shorthand forms.
 */
export interface MergeActionObject<Row extends object = DataRow> {
  type?: string;
  set?: Assignment<Row>;
  values?: Assignment<Row>;
  update?: Assignment<Row>;
  delete?: boolean;
  insert?: Assignment<Row>;
  doNothing?: boolean;
}

/**
 * A WHEN ... THEN branch.
 *
 * `condition` is the canonical spelling. `and` and `when` are accepted as
 * aliases. The action may be supplied as `action`, `then`, or by using one of
 * the shorthand properties (`update`, `delete`, `insert`, `doNothing`).
 */
export interface MergeBranch<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> {
  condition?: BranchCondition<TargetRow, SourceRow>;
  and?: BranchCondition<TargetRow, SourceRow>;
  when?: BranchCondition<TargetRow, SourceRow>;

  action?:
    | MergeAction<TargetRow>
    | MergeAction<DataRow>
    | MergeActionObject<TargetRow>
    | string;
  then?:
    | MergeAction<TargetRow>
    | MergeAction<DataRow>
    | MergeActionObject<TargetRow>
    | string;

  update?: Assignment<TargetRow>;
  delete?: boolean;
  insert?: Assignment<TargetRow>;
  doNothing?: boolean;

  type?: string;
  set?: Assignment<TargetRow>;
  values?: Assignment<TargetRow>;
}

export type UniqueTiming = "immediate" | "deferred";

/**
 * `distinct` is the SQL-standard NULLS DISTINCT mode: any key containing NULL
 * is exempt from uniqueness. `not distinct` treats NULL as an ordinary,
 * equal key component.
 */
export type NullsMode = "distinct" | "not distinct";

export interface UniqueIndex<Row extends object = DataRow> {
  name?: string;
  columns?: readonly string[] | string;
  fields?: readonly string[];
  column?: string;
  /**
   * Optional derived key. For NULLS DISTINCT, an array result containing a
   * nullish component is treated like a key containing NULL.
   */
  key?: (row: Row) => unknown;
  timing?: UniqueTiming | string;
  mode?: UniqueTiming | string;
  deferred?: boolean;
  nulls?: NullsMode | string;
  nullsDistinct?: boolean;
  nullsNotDistinct?: boolean;
}

export type UniqueIndexInput<Row extends object = DataRow> =
  | UniqueIndex<Row>
  | readonly string[]
  | string;

export interface MergeTableInput<Row extends object = DataRow> {
  readonly rows: readonly Row[];
}

/**
 * Statement used by `executeMerge`.
 *
 * `whenNotMatched` is an alias for `whenNotMatchedByTarget`, matching the
 * shortened SQL spelling.
 */
export interface MergeStatement<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> {
  target: readonly TargetRow[] | MergeTableInput<TargetRow>;
  source: readonly SourceRow[] | MergeTableInput<SourceRow>;
  on: MatchPredicate<NoInfer<TargetRow>, NoInfer<SourceRow>>;

  whenMatched?:
    | MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>
    | readonly MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>[];
  whenNotMatched?:
    | MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>
    | readonly MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>[];
  whenNotMatchedByTarget?:
    | MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>
    | readonly MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>[];
  whenNotMatchedBySource?:
    | MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>
    | readonly MergeBranch<NoInfer<TargetRow>, NoInfer<SourceRow>>[];

  uniqueIndexes?: readonly UniqueIndexInput<NoInfer<TargetRow>>[];
  uniqueConstraints?: readonly UniqueIndexInput<NoInfer<TargetRow>>[];
  constraints?: readonly UniqueIndexInput<NoInfer<TargetRow>>[];
}

export interface MergeStats {
  readonly targetRowsBefore: number;
  readonly targetRowsAfter: number;
  readonly sourceRows: number;
  /** Number of (targetRow, sourceRow) pairs found by ON. */
  readonly matchedPairs: number;
  readonly matchedSourceRows: number;
  readonly unmatchedSourceRows: number;
  readonly unmatchedTargetRows: number;
  readonly updated: number;
  readonly deleted: number;
  readonly inserted: number;
  readonly doNothing: number;
}

export interface MergeResult<Row extends object = DataRow> {
  /** Final target rows, in original order with inserted rows appended. */
  readonly rows: Row[];
  /** Alias for `rows`, convenient when the statement itself has `target`. */
  readonly target: Row[];
  readonly inserted: Row[];
  readonly updated: Row[];
  readonly deleted: Row[];
  readonly insertedRows: Row[];
  readonly updatedRows: Row[];
  readonly deletedRows: Row[];
  readonly stats: MergeStats;
}

export interface UniqueViolation<Row extends object = DataRow> {
  readonly index: string;
  readonly key: readonly unknown[];
  readonly rowIndexes: readonly [number, number];
  readonly rows: readonly [Row, Row];
}
