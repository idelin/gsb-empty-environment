export type MergeErrorCode =
  | "CARDINALITY_VIOLATION"
  | "UNIQUE_VIOLATION"
  | "INVALID_MERGE_STATEMENT";

export const CARDINALITY_VIOLATION = "CARDINALITY_VIOLATION" as const;
export const UNIQUE_VIOLATION = "UNIQUE_VIOLATION" as const;
export const INVALID_MERGE_STATEMENT = "INVALID_MERGE_STATEMENT" as const;

export class MergeError extends Error {
  readonly code: MergeErrorCode;
  readonly details: Readonly<Record<string, unknown>>;

  constructor(
    code: MergeErrorCode,
    message: string,
    details: Readonly<Record<string, unknown>> = {},
  ) {
    super(message);
    this.name = new.target.name;
    this.code = code;
    this.details = details;
  }
}

export interface CardinalityViolationDetails<
  TargetRow extends object = object,
  SourceRow extends object = object,
> {
  readonly targetIndex: number;
  readonly sourceIndex: number;
  readonly targetRow: TargetRow;
  readonly sourceRow: SourceRow;
  readonly action: "update" | "delete";
}

export class CardinalityViolationError<
  TargetRow extends object = object,
  SourceRow extends object = object,
> extends MergeError {
  readonly targetIndex: number;
  readonly sourceIndex: number;
  readonly targetRow: TargetRow;
  readonly sourceRow: SourceRow;
  readonly action: "update" | "delete";

  constructor(details: CardinalityViolationDetails<TargetRow, SourceRow>) {
    super(
      "CARDINALITY_VIOLATION",
      `MERGE cardinality violation (CARDINALITY_VIOLATION): target row ${details.targetIndex} was already modified by an earlier source row and cannot be modified again by source row ${details.sourceIndex}`,
      details as unknown as Record<string, unknown>,
    );
    this.targetIndex = details.targetIndex;
    this.sourceIndex = details.sourceIndex;
    this.targetRow = details.targetRow;
    this.sourceRow = details.sourceRow;
    this.action = details.action;
  }
}

export interface UniqueViolationDetails<Row extends object = object> {
  readonly index: string;
  readonly key: readonly unknown[];
  readonly rowIndexes: readonly [number, number];
  readonly rows: readonly [Row, Row];
}

export class UniqueViolationError<
  Row extends object = object,
> extends MergeError {
  readonly index: string;
  readonly key: readonly unknown[];
  readonly rowIndexes: readonly [number, number];
  readonly rows: readonly [Row, Row];

  constructor(details: UniqueViolationDetails<Row>) {
    super(
      "UNIQUE_VIOLATION",
      `Unique constraint "${details.index}" violated (UNIQUE_VIOLATION) by target rows ${details.rowIndexes[0]} and ${details.rowIndexes[1]}`,
      details as unknown as Record<string, unknown>,
    );
    this.index = details.index;
    this.key = details.key;
    this.rowIndexes = details.rowIndexes;
    this.rows = details.rows;
  }
}
