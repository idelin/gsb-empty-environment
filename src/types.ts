export type Uri = string;

export interface Position {
  line: number;
  character: number;
}

export interface Range {
  start: Position;
  end: Position;
}

export type DiagnosticSeverity = "error" | "warning" | "information" | "hint";

export interface Diagnostic {
  range: Range;
  message: string;
  severity: DiagnosticSeverity;
  source?: string;
}

export interface DiagnosticsChangedEvent {
  uri: Uri;
  diagnostics: readonly Diagnostic[];
}
