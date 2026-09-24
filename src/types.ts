export type PlaceholderKind = "literal" | "formula";

export interface FormulaContext {
  get(id: string): string;
}

export type FormulaCompute = (ctx: FormulaContext) => string;

export type MirrorTransform = (source: string) => string;

export interface FormulaSpec {
  kind: "formula";
  /** Explicitly declared dependency placeholder ids. */
  dependsOn: readonly string[];
  /** Pure function over the *computed results* of other placeholders. */
  compute: FormulaCompute;
}

export interface LiteralSpec {
  kind: "literal";
  initial?: string;
}

export type PlaceholderSpec = FormulaSpec | LiteralSpec;

export interface MirrorOptions {
  /** Pure function applied to the mirrored placeholder's computed result. */
  transform?: MirrorTransform;
}

export interface EditResult {
  /** Rendered plain text after the edit. */
  text: string;
  /** Placeholder spans after the edit. */
  spans: ReadonlyMap<string, readonly RenderedSpan[]>;
}

export interface RenderedSpan {
  placeholderId: string;
  start: number;
  end: string | null;
  /**
   * When true this span is a mirror (read-only reference).
   */
  mirror: boolean;
  /** Id of the mirror occurrence node (unique even when one placeholder is mirrored twice). */
  occurrenceId?: string;
}

export interface Snapshot {
  text: string;
  root: unknown;
}
