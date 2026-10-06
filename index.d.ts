/**
 * breakpoint-anchor — statement-identity anchored breakpoints for
 * TypeScript/JavaScript sources, tracked across edit chains.
 */

export interface Position {
  /** 1-based line number. */
  line: number;
  /** 1-based column number. */
  column: number;
}

export interface TextEdit {
  /** 0-based start offset, inclusive. */
  start: number;
  /** 0-based end offset, exclusive. */
  end: number;
  /** Replacement text (may be empty for deletions). */
  text: string;
}

export type StatementKind = string;

export interface Statement {
  /** Stable identity carried across versions by the identity model. */
  id: string;
  /** 0-based start offset of the statement in the current source. */
  start: number;
  /** 0-based end offset (exclusive) in the current source. */
  end: number;
  kind: StatementKind;
  /** Nesting depth; 0 for top-level statements. */
  depth: number;
  /** Index of the enclosing statement in the same parse, or -1. */
  parent: number;
  /** Content key: kind + normalized token text. */
  key: string;
}

export type BreakpointStatus = 'bound' | 'unbound';

export interface Breakpoint {
  id: string;
  /**
   * 'bound' while the anchored statement exists in the current version;
   * 'unbound' after it was deleted. Unbound breakpoints never slide onto
   * unrelated lines; they rebind if the statement is restored.
   */
  status: BreakpointStatus;
  /** Id of the anchored statement (retained even while unbound). */
  statementId: string;
  /** Current 1-based line, or null while unbound. */
  line: number | null;
  /** Current 1-based column, or null while unbound. */
  column: number | null;
  /** Last position at which the breakpoint was bound. */
  lastKnown: { line: number; column: number; version: number };
}

export interface VersionMappedBreakpoint {
  status: BreakpointStatus;
  line: number | null;
  column: number | null;
}

export type StoreEvent =
  | { type: 'breakpoint-added'; breakpoint: Breakpoint }
  | { type: 'breakpoint-removed'; breakpointId: string }
  | {
      type: 'version';
      version: number;
      edits: TextEdit[];
      deletedStatementIds: string[];
      reboundBreakpointIds: string[];
      unboundBreakpointIds: string[];
    };

export interface BreakpointStoreOptions {
  /** How many versions to retain for cross-version mapping. Default 64. */
  historyLimit?: number;
  /** How many deleted statements to remember for revival. Default 256. */
  tombstoneLimit?: number;
}

export class BreakpointStore {
  constructor(source: string, options?: BreakpointStoreOptions);

  /** Current source text. */
  getSource(): string;
  /** Current version number; increments once per applied edit batch. */
  getVersion(): number;

  /** Statements of the current version, in document order. */
  getStatements(): Statement[];
  /** Innermost statement covering a 1-based position, or null. */
  getStatementAt(line: number, column: number): Statement | null;

  /**
   * Bind a breakpoint to the innermost statement at a 1-based position.
   * Returns null when no statement covers the position.
   */
  setBreakpoint(line: number, column: number): Breakpoint | null;
  removeBreakpoint(bpId: string): boolean;
  /** All breakpoints resolved against the current version. */
  getBreakpoints(): Breakpoint[];

  /** Apply one edit (0-based offsets into the current source). */
  applyEdit(edit: TextEdit): number;
  /**
   * Apply several edits of one user operation, expressed in the coordinate
   * system of the current source. Counts as a single version step.
   */
  applyEdits(edits: TextEdit[]): number;
  /** Replace the whole source (e.g. loading another file revision). */
  setSource(nextSource: string): number;

  /**
   * Map a breakpoint onto a retained version. Returns null when the
   * breakpoint or version is unknown; otherwise the bound position, or
   * { status: 'unbound' } when the anchored statement did not exist then.
   */
  mapBreakpointToVersion(bpId: string, version: number): VersionMappedBreakpoint | null;

  subscribe(listener: (event: StoreEvent) => void): () => void;
}

export interface ParsedStatement {
  start: number;
  end: number;
  kind: StatementKind;
  depth: number;
  parent: number;
  key: string;
}

/** Parse a source string into statements (without identity assignment). */
export function parseStatements(source: string): ParsedStatement[];
/** Index of the innermost statement containing an offset, or -1. */
export function innermostStatementAt(statements: ParsedStatement[], offset: number): number;

export interface MatchResult {
  matched: Array<ParsedStatement & { id: string }>;
  deleted: Array<{ id: string; key: string }>;
}

/** The explicit statement identity model. */
export class IdentityModel {
  constructor(options?: { tombstoneLimit?: number });
  readonly tombstoneCount: number;
  assignInitial(statements: ParsedStatement[]): Array<ParsedStatement & { id: string }>;
  matchStatements(
    prev: Array<ParsedStatement & { id: string }>,
    next: ParsedStatement[],
    edits: TextEdit[],
  ): MatchResult;
  clearTombstones(): void;
}

export class LineIndex {
  constructor(text: string);
  offsetToPosition(offset: number): Position;
  positionToOffset(line: number, column: number): number;
}
