/** 0-based position (LSP style). */
export interface Position {
  line: number;
  character: number;
}

/** Half-open range: [start, end). */
export interface Range {
  start: Position;
  end: Position;
}

export interface Heading {
  level: number;
  text: string;
  range: Range;
}

export interface Reference {
  /** Name as written in the source (not normalized). */
  name: string;
  range: Range;
  resolved: boolean;
  /** The definition this reference resolves to, when resolved. */
  target?: Definition;
}

export interface Definition {
  /** Name as written in the source (not normalized). */
  name: string;
  /** Dedented content, continuation lines joined with '\n'. */
  content: string;
  /** Whole definition block, first line through last continuation line. */
  range: Range;
  /** The `[^name]` marker on the first line. */
  nameRange: Range;
  /** References occurring inside the definition content. */
  references: Reference[];
}

export type DiagnosticCode =
  | 'duplicate-definition'
  | 'undefined-reference'
  | 'circular-footnote';

export interface Diagnostic {
  code: DiagnosticCode;
  /** Offending name as written in the source. */
  name: string;
  message: string;
  range: Range;
}

export interface ParseResult {
  headings: Heading[];
  /** All references in document order, including those inside definitions. */
  references: Reference[];
  /** All definitions in document order, including duplicates. */
  definitions: Definition[];
  diagnostics: Diagnostic[];
}

export interface TextEdit {
  range: Range;
  newText: string;
}

export type AffectedChangeKind =
  | 'added'
  | 'removed'
  | 'resolution-changed'
  | 'target-changed';

export interface AffectedReference {
  change: AffectedChangeKind;
  /** Name as written in the source. */
  name: string;
  /** Current range; for 'removed' this is the range before the edit. */
  range: Range;
  /** Range before the edit, when the reference still exists. */
  previousRange?: Range;
}

export interface UpdateResult {
  /**
   * References whose semantic state changed: added, removed, flipped
   * resolved/unresolved, or re-targeted to a changed definition.
   * Pure position shifts are not reported.
   */
  affectedReferences: AffectedReference[];
  /** Full diagnostic list after the edit. */
  diagnostics: Diagnostic[];
}
