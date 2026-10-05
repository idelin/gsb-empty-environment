import { SourceRange } from "./types";

export function rangesEqual(a: SourceRange, b: SourceRange): boolean {
  return (
    a.start.line === b.start.line &&
    a.start.column === b.start.column &&
    a.end.line === b.end.line &&
    a.end.column === b.end.column
  );
}

export function rangeKey(file: string, range: SourceRange): string {
  return [
    file,
    range.start.line,
    range.start.column,
    range.end.line,
    range.end.column,
  ].join(":");
}

export function normalizeStatementText(text: string): string {
  return text.replace(/\s+/g, "");
}

export function formatRange(range: SourceRange): string {
  return (
    range.start.line +
    ":" +
    range.start.column +
    "-" +
    range.end.line +
    ":" +
    range.end.column
  );
}
