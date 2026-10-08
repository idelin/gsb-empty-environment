export interface Position {
  line: number;
  character: number;
}

export interface Range {
  start: Position;
  end: Position;
}

export type DiagnosticSeverity = 'error' | 'warning';

export type DiagnosticCode =
  | 'undefined-reference'
  | 'duplicate-definition'
  | 'circular-footnote';

export interface Diagnostic {
  code: DiagnosticCode;
  severity: DiagnosticSeverity;
  message: string;
  range: Range;
  relatedNames?: string[];
}

export interface Heading {
  level: number;
  text: string;
  line: number;
  range: Range;
}

export interface Reference {
  /** Stable identity: `name` for the first occurrence, `name#N` for the Nth. */
  key: string;
  name: string;
  index: number;
  line: number;
  range: Range;
  inDefinitionBody: boolean;
}

export interface FootnoteDefinition {
  name: string;
  body: string;
  line: number;
  /** Range of the definition name (without the `[^`/`]:` markers). */
  range: Range;
  /** References found inside this definition's body. */
  references: Reference[];
}

/** Content-only parse of a single line; absolute line numbers are assigned at assembly. */
export interface LineInfo {
  heading?: {
    level: number;
    text: string;
    startCol: number;
    endCol: number;
  };
  definition?: {
    name: string;
    body: string;
    nameStartCol: number;
    nameEndCol: number;
    bodyStartCol: number;
    endCol: number;
  };
  references: {
    name: string;
    startCol: number;
    endCol: number;
    inDefinitionBody: boolean;
  }[];
}

export interface ParsedDocument {
  headings: Heading[];
  references: Reference[];
  definitions: FootnoteDefinition[];
  /** First definition wins for each name. */
  definitionMap: Map<string, FootnoteDefinition>;
  /** Definitions dropped because a definition with the same name came first. */
  duplicates: FootnoteDefinition[];
  diagnostics: Diagnostic[];
  /** Diagnostics attached to individual references, keyed by reference key. */
  diagnosticsByReference: Map<string, Diagnostic[]>;
}
