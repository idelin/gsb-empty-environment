import type {
  Assignment,
  BranchCondition,
  DataRow,
  InsertAction,
  MergeActionKind,
  MergeBranch,
  MergeContext,
  MergePhase,
  MergeResult,
  MergeStatement,
  MergeStats,
  UniqueIndexInput,
  UpdateAction,
  DeleteAction,
  DoNothingAction,
} from "./types.js";
import {
  MergeError,
  CardinalityViolationError,
} from "./errors.js";
import {
  normalizeUniqueIndexes,
  validateNormalizedUniqueConstraints,
  type NormalizedUniqueIndex,
} from "./constraints.js";

interface Slot<Row extends object> {
  readonly id: number;
  row: Row;
  deleted: boolean;
}

type NormalizedAction<Row extends object> =
  | { readonly type: "update"; readonly set: Assignment<Row> }
  | { readonly type: "delete" }
  | { readonly type: "insert"; readonly values?: Assignment<Row> }
  | { readonly type: "doNothing" };

interface NormalizedBranch<
  TargetRow extends object,
  SourceRow extends object,
> {
  readonly condition: BranchCondition<TargetRow, SourceRow> | undefined;
  readonly action: NormalizedAction<TargetRow>;
}

interface ExecutionState<Row extends object> {
  readonly slots: Slot<Row>[];
  readonly snapshot: Slot<Row>[];
  readonly modifiedTargets: Set<number>;
  readonly indexes: readonly NormalizedUniqueIndex<Row>[];
  readonly immediateIndexes: readonly NormalizedUniqueIndex<Row>[];
  nextId: number;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function cloneRow<Row extends object>(row: Row): Row {
  return { ...row };
}

function readRows<Row extends object>(
  input: readonly Row[] | { readonly rows: readonly Row[] },
  label: string,
): Row[] {
  const rows = Array.isArray(input)
    ? input
    : isRecord(input) && Array.isArray(input.rows)
      ? input.rows
      : null;

  if (!rows) {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      `${label} must be an array of rows or an object with a rows array`,
      { input },
    );
  }

  return rows.map((row, index) => {
    if (!isRecord(row)) {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        `${label}[${index}] is not a plain row object`,
        { index, row },
      );
    }
    return cloneRow(row) as Row;
  });
}

function toArray<T>(value: T | readonly T[] | undefined): readonly T[] {
  if (value === undefined) {
    return [];
  }
  return Array.isArray(value) ? value : [value as T];
}

function normalizeKind(raw: unknown): MergeActionKind {
  if (typeof raw !== "string") {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE branch action type must be a string",
      { type: raw },
    );
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
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        `Unknown MERGE branch action "${raw}"`,
        { type: raw },
      );
  }
}

function inferredKind<
  TargetRow extends object,
  SourceRow extends object,
>(
  raw: Record<string, unknown>,
  branch: MergeBranch<TargetRow, SourceRow>,
): MergeActionKind {
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
  throw new MergeError(
    "INVALID_MERGE_STATEMENT",
    "MERGE branch does not define an action",
    { branch },
  );
}

function firstDefined(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined) {
      return value;
    }
  }
  return undefined;
}

function normalizeAction<
  TargetRow extends object,
  SourceRow extends object,
>(
  branch: MergeBranch<TargetRow, SourceRow>,
  phase: MergePhase,
): NormalizedAction<TargetRow> {
  const candidate = firstDefined(branch.action, branch.then);
  let raw: Record<string, unknown>;

  if (candidate !== undefined) {
    if (typeof candidate === "string") {
      raw = { type: candidate };
    } else if (isRecord(candidate)) {
      raw = candidate;
    } else {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "MERGE branch action must be a string or object",
        { branch },
      );
    }
  } else if (branch.update !== undefined) {
    raw = { type: "update", update: branch.update };
  } else if (branch.delete === true) {
    raw = { type: "delete" };
  } else if (branch.insert !== undefined) {
    raw = { type: "insert", insert: branch.insert };
  } else if (branch.doNothing === true) {
    raw = { type: "doNothing" };
  } else if (
    branch.type !== undefined
    || branch.set !== undefined
    || branch.values !== undefined
  ) {
    raw = branch as unknown as Record<string, unknown>;
  } else {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE branch does not define an action",
      { branch },
    );
  }

  const kind = inferredKind(raw, branch);

  if (kind === "update") {
    if (phase === "notMatchedByTarget") {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "WHEN NOT MATCHED BY TARGET cannot perform UPDATE",
      );
    }
    const set = firstDefined(
      raw.set,
      raw.values,
      raw.update,
      branch.set,
      branch.update,
    );
    if (set === undefined) {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "UPDATE action requires a set assignment",
      );
    }
    return { type: "update", set: set as Assignment<TargetRow> };
  }

  if (kind === "delete") {
    if (phase === "notMatchedByTarget") {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "WHEN NOT MATCHED BY TARGET cannot perform DELETE",
      );
    }
    return { type: "delete" };
  }

  if (kind === "insert") {
    if (phase !== "notMatchedByTarget") {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "INSERT is only valid in WHEN NOT MATCHED [BY TARGET]",
      );
    }
    const values = firstDefined(
      raw.values,
      raw.set,
      raw.insert,
      branch.values,
      branch.insert,
    );
    return {
      type: "insert",
      values: values as Assignment<TargetRow> | undefined,
    };
  }

  return { type: "doNothing" };
}

function normalizeBranches<
  TargetRow extends object,
  SourceRow extends object,
>(
  branches:
    | MergeBranch<TargetRow, SourceRow>
    | readonly MergeBranch<TargetRow, SourceRow>[]
    | undefined,
  phase: MergePhase,
): NormalizedBranch<TargetRow, SourceRow>[] {
  return toArray(branches).map((branch) => {
    if (!isRecord(branch)) {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "MERGE branch must be an object",
        { branch },
      );
    }

    const condition = firstDefined(
      branch.condition,
      branch.and,
      branch.when,
    ) as BranchCondition<TargetRow, SourceRow> | undefined;
    if (
      condition !== undefined
      && typeof condition !== "boolean"
      && typeof condition !== "function"
    ) {
      throw new MergeError(
        "INVALID_MERGE_STATEMENT",
        "MERGE branch condition must be a boolean or predicate function",
        { branch },
      );
    }

    return {
      condition,
      action: normalizeAction(branch, phase),
    };
  });
}

function conditionValue<
  TargetRow extends object,
  SourceRow extends object,
>(
  condition: BranchCondition<TargetRow, SourceRow> | undefined,
  phase: MergePhase,
  target: TargetRow | null,
  source: SourceRow | null,
  context: MergeContext<TargetRow, SourceRow>,
): boolean {
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

function selectBranch<
  TargetRow extends object,
  SourceRow extends object,
>(
  branches: readonly NormalizedBranch<TargetRow, SourceRow>[],
  phase: MergePhase,
  target: TargetRow | null,
  source: SourceRow | null,
  context: MergeContext<TargetRow, SourceRow>,
): NormalizedBranch<TargetRow, SourceRow> | undefined {
  for (const branch of branches) {
    if (conditionValue(branch.condition, phase, target, source, context)) {
      return branch;
    }
  }
  return undefined;
}

function resolveAssignment<
  TargetRow extends object,
  SourceRow extends object,
>(
  assignment: Assignment<TargetRow> | undefined,
  phase: MergePhase,
  target: TargetRow | null,
  source: SourceRow | null,
  context: MergeContext<TargetRow, SourceRow>,
): Partial<TargetRow> {
  if (assignment === undefined) {
    return source ? cloneRow(source) as Partial<TargetRow> : {};
  }

  const invoke = (resolver: (...args: any[]) => unknown): unknown => {
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

  let result: unknown;
  if (typeof assignment === "function") {
    result = invoke(assignment as (...args: any[]) => unknown);
  } else if (isRecord(assignment)) {
    const resolved: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(assignment)) {
      resolved[key] = typeof value === "function"
        ? invoke(value as (...args: any[]) => unknown)
        : value;
    }
    result = resolved;
  } else {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE assignment must be an object or resolver function",
      { assignment },
    );
  }

  if (!isRecord(result)) {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE assignment resolver must return a row object",
      { result },
    );
  }
  return result as Partial<TargetRow>;
}

function activeSlots<Row extends object>(
  state: ExecutionState<Row>,
): Slot<Row>[] {
  return state.slots.filter((slot) => !slot.deleted);
}

function checkImmediate<Row extends object>(state: ExecutionState<Row>): void {
  if (state.immediateIndexes.length === 0) {
    return;
  }
  const active = activeSlots(state);
  validateNormalizedUniqueConstraints(
    active.map((slot) => slot.row),
    state.immediateIndexes,
    active.map((slot) => slot.id),
  );
}

function cardinalityViolation<
  TargetRow extends object,
  SourceRow extends object,
>(
  slot: Slot<TargetRow>,
  source: SourceRow,
  sourceIndex: number,
  action: "update" | "delete",
): never {
  throw new CardinalityViolationError({
    targetIndex: slot.id,
    sourceIndex,
    targetRow: cloneRow(slot.row),
    sourceRow: cloneRow(source),
    action,
  });
}

function executeMatchedAction<
  TargetRow extends object,
  SourceRow extends object,
>(
  state: ExecutionState<TargetRow>,
  slot: Slot<TargetRow>,
  source: SourceRow,
  sourceIndex: number,
  action: NormalizedAction<TargetRow>,
  updatedRows: TargetRow[],
  deletedRows: TargetRow[],
  stats: MutableStats,
): void {
  const context: MergeContext<TargetRow, SourceRow> = {
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
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "INSERT cannot be used in a WHEN MATCHED branch",
    );
  }

  if (state.modifiedTargets.has(slot.id)) {
    cardinalityViolation(slot, source, sourceIndex, action.type);
  }

  if (action.type === "update") {
    const patch = resolveAssignment(action.set, "matched", slot.row, source, context);
    slot.row = { ...slot.row, ...patch } as TargetRow;
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

  throw new MergeError(
    "INVALID_MERGE_STATEMENT",
    `Unsupported matched action "${(action as NormalizedAction<TargetRow>).type}"`,
  );
}

function executeSourceInsert<
  TargetRow extends object,
  SourceRow extends object,
>(
  state: ExecutionState<TargetRow>,
  source: SourceRow,
  sourceIndex: number,
  action: NormalizedAction<TargetRow>,
  insertedRows: TargetRow[],
  stats: MutableStats,
): void {
  if (action.type === "doNothing") {
    stats.doNothing += 1;
    return;
  }
  if (action.type !== "insert") {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "Only INSERT or DO NOTHING is valid for an unmatched source row",
    );
  }

  const context: MergeContext<TargetRow, SourceRow> = {
    phase: "notMatchedByTarget",
    target: null,
    source,
    sourceIndex,
  };
  const values = resolveAssignment(
    action.values,
    "notMatchedByTarget",
    null,
    source,
    context,
  );
  const row = { ...values } as TargetRow;
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

function executeTargetAction<TargetRow extends object>(
  state: ExecutionState<TargetRow>,
  slot: Slot<TargetRow>,
  action: NormalizedAction<TargetRow>,
  updatedRows: TargetRow[],
  deletedRows: TargetRow[],
  stats: MutableStats,
): void {
  const context: MergeContext<TargetRow, DataRow> = {
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
    slot.row = { ...slot.row, ...patch } as TargetRow;
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

  throw new MergeError(
    "INVALID_MERGE_STATEMENT",
    "INSERT cannot be used in WHEN NOT MATCHED BY SOURCE",
  );
}

interface MutableStats {
  targetRowsBefore: number;
  targetRowsAfter: number;
  sourceRows: number;
  matchedPairs: number;
  matchedSourceRows: number;
  unmatchedSourceRows: number;
  unmatchedTargetRows: number;
  updated: number;
  deleted: number;
  inserted: number;
  doNothing: number;
}

function collectIndexes<
  TargetRow extends object,
  SourceRow extends object,
>(
  statement: MergeStatement<TargetRow, SourceRow>,
): UniqueIndexInput<TargetRow>[] {
  const collect = (
    value:
      | readonly UniqueIndexInput<TargetRow>[]
      | UniqueIndexInput<TargetRow>
      | undefined,
  ): UniqueIndexInput<TargetRow>[] => {
    if (value === undefined) {
      return [];
    }
    return Array.isArray(value)
      ? [...value] as UniqueIndexInput<TargetRow>[]
      : [value as UniqueIndexInput<TargetRow>];
  };

  return [
    ...collect(statement.uniqueIndexes),
    ...collect(statement.uniqueConstraints),
    ...collect(statement.constraints),
  ];
}

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
export function executeMerge(
  statement: MergeStatement<DataRow, DataRow> & {
    readonly target: readonly [] | { readonly rows: readonly never[] };
  },
): MergeResult<DataRow>;
export function executeMerge<TargetRow extends object = DataRow>(
  statement: MergeStatement<TargetRow, DataRow> & {
    readonly source: readonly [] | { readonly rows: readonly never[] };
  },
): MergeResult<TargetRow>;
export function executeMerge<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
>(
  statement: MergeStatement<TargetRow, SourceRow>,
): MergeResult<TargetRow>;
export function executeMerge<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
>(
  statement: MergeStatement<TargetRow, SourceRow>,
): MergeResult<TargetRow> {
  if (!isRecord(statement)) {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE statement must be an object",
      { statement },
    );
  }
  if (typeof statement.on !== "function") {
    throw new MergeError(
      "INVALID_MERGE_STATEMENT",
      "MERGE statement requires an on(target, source) predicate",
    );
  }

  const targetRows = readRows<TargetRow>(statement.target, "target");
  const sourceRows = readRows<SourceRow>(statement.source, "source");
  const matchedBranches = normalizeBranches(statement.whenMatched, "matched");
  const notMatchedByTargetBranches = normalizeBranches(
    [
      ...toArray(statement.whenNotMatchedByTarget),
      ...toArray(statement.whenNotMatched),
    ],
    "notMatchedByTarget",
  );
  const notMatchedBySourceBranches = normalizeBranches(
    statement.whenNotMatchedBySource,
    "notMatchedBySource",
  );
  const indexes = normalizeUniqueIndexes(collectIndexes(statement));

  const snapshot: Slot<TargetRow>[] = targetRows.map((row, index) => ({
    id: index,
    row,
    deleted: false,
  }));
  const state: ExecutionState<TargetRow> = {
    slots: [...snapshot],
    snapshot,
    modifiedTargets: new Set<number>(),
    indexes,
    immediateIndexes: indexes.filter((index) => index.timing === "immediate"),
    nextId: snapshot.length,
  };

  const stats: MutableStats = {
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

  const insertedRows: TargetRow[] = [];
  const updatedRows: TargetRow[] = [];
  const deletedRows: TargetRow[] = [];

  const matchesBySource: Slot<TargetRow>[][] = sourceRows.map((source, sourceIndex) => {
    const matches: Slot<TargetRow>[] = [];
    for (const target of snapshot) {
      const context: MergeContext<TargetRow, SourceRow> = {
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

  const matchedTargetIds = new Set<number>();
  for (const matches of matchesBySource) {
    for (const target of matches) {
      matchedTargetIds.add(target.id);
    }
  }
  stats.matchedPairs = matchesBySource.reduce(
    (total, matches) => total + matches.length,
    0,
  );
  stats.unmatchedTargetRows = snapshot.length - matchedTargetIds.size;

  for (let sourceIndex = 0; sourceIndex < sourceRows.length; sourceIndex += 1) {
    const source = sourceRows[sourceIndex]!;
    const matches = matchesBySource[sourceIndex]!;

    if (matches.length === 0) {
      stats.unmatchedSourceRows += 1;
      const branch = selectBranch(
        notMatchedByTargetBranches,
        "notMatchedByTarget",
        null,
        source,
        {
          phase: "notMatchedByTarget",
          target: null,
          source,
          sourceIndex,
        },
      );
      if (branch) {
        executeSourceInsert(
          state,
          source,
          sourceIndex,
          branch.action,
          insertedRows,
          stats,
        );
      }
      continue;
    }

    stats.matchedSourceRows += 1;
    for (const target of matches) {
      const branch = selectBranch(
        matchedBranches,
        "matched",
        target.row,
        source,
        {
          phase: "matched",
          target: target.row,
          source,
          targetIndex: target.id,
          sourceIndex,
        },
      );
      if (branch) {
        executeMatchedAction(
          state,
          target,
          source,
          sourceIndex,
          branch.action,
          updatedRows,
          deletedRows,
          stats,
        );
      }
    }
  }

  for (const target of snapshot) {
    if (matchedTargetIds.has(target.id) || target.deleted) {
      continue;
    }
    const branch = selectBranch(
      notMatchedBySourceBranches,
      "notMatchedBySource",
      target.row,
      null,
      {
        phase: "notMatchedBySource",
        target: target.row,
        source: null,
        targetIndex: target.id,
      },
    );
    if (branch) {
      executeTargetAction(
        state,
        target,
        branch.action,
        updatedRows,
        deletedRows,
        stats,
      );
    }
  }

  const active = activeSlots(state);
  validateNormalizedUniqueConstraints(
    active.map((slot) => slot.row),
    state.indexes,
    active.map((slot) => slot.id),
  );

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
    stats: stats as MergeStats,
  };
}

export function mergeRows(
  statement: MergeStatement<DataRow, DataRow> & {
    readonly target: readonly [] | { readonly rows: readonly never[] };
  },
): DataRow[];
export function mergeRows<TargetRow extends object = DataRow>(
  statement: MergeStatement<TargetRow, DataRow> & {
    readonly source: readonly [] | { readonly rows: readonly never[] };
  },
): TargetRow[];
export function mergeRows<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
>(
  statement: MergeStatement<TargetRow, SourceRow>,
): TargetRow[] {
  return executeMerge(statement).rows;
}

export const mergeInto = executeMerge;
export const merge = executeMerge;
export const runMerge = executeMerge;
export const applyMerge = executeMerge;
export const executeSqlMerge = executeMerge;

export function update<Row extends object = DataRow>(
  set: Assignment<NoInfer<Row>>,
): UpdateAction<Row> {
  return { type: "update", set };
}

export function deleteRow(): DeleteAction {
  return { type: "delete" };
}

export function insert<Row extends object = DataRow>(
  values?: Assignment<NoInfer<Row>>,
): InsertAction<Row> {
  return values === undefined
    ? { type: "insert" }
    : { type: "insert", values };
}

export function doNothing(): DoNothingAction {
  return { type: "doNothing" };
}

export const noop = doNothing;

export interface MergeTableOptions<Row extends object = DataRow> {
  rows?: readonly Row[];
  uniqueIndexes?: readonly UniqueIndexInput<Row>[];
  uniqueConstraints?: readonly UniqueIndexInput<Row>[];
}

export type MergeOperation<
  TargetRow extends object = DataRow,
  SourceRow extends object = DataRow,
> = Omit<
  MergeStatement<TargetRow, SourceRow>,
  "target"
>;

/**
 * Small stateful wrapper for callers that want a table object. State is only
 * replaced after a successful MERGE, so failed statements leave the table
 * untouched.
 */
export class MergeTable<Row extends object = DataRow> {
  private _rows: Row[];
  private readonly _indexes: readonly UniqueIndexInput<Row>[];

  constructor(
    options: MergeTableOptions<Row> | readonly Row[] = [],
  ) {
    const config: MergeTableOptions<Row> = Array.isArray(options)
      ? { rows: options }
      : options as MergeTableOptions<Row>;
    this._rows = (config.rows ?? []).map((row) => cloneRow(row));
    this._indexes = [
      ...(config.uniqueIndexes ?? []),
      ...(config.uniqueConstraints ?? []),
    ];
  }

  get rows(): readonly Row[] {
    return this._rows;
  }

  get uniqueIndexes(): readonly UniqueIndexInput<Row>[] {
    return this._indexes;
  }

  merge<SourceRow extends object = DataRow>(
    statement: MergeOperation<Row, SourceRow>,
  ): MergeResult<Row> {
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

  applyMerge<SourceRow extends object = DataRow>(
    statement: MergeOperation<Row, SourceRow>,
  ): MergeResult<Row> {
    return this.merge<SourceRow>(statement);
  }
}
