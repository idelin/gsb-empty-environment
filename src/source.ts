import type { Span } from "./ast.ts";

/** A position in the template source, 1-based line and column. */
export interface Position {
  line: number;
  column: number;
  offset: number;
}

/** Maps character offsets to line/column positions for a template. */
export class Source {
  private readonly lineStarts: number[];
  readonly text: string;

  constructor(text: string) {
    this.text = text;
    this.lineStarts = [0];
    for (let i = 0; i < text.length; i++) {
      if (text[i] === "\n") this.lineStarts.push(i + 1);
    }
  }

  positionAt(offset: number): Position {
    // Binary search for the greatest line start <= offset.
    let lo = 0;
    let hi = this.lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.lineStarts[mid] <= offset) lo = mid;
      else hi = mid - 1;
    }
    return { line: lo + 1, column: offset - this.lineStarts[lo] + 1, offset };
  }

  /** "line L, column C" for the start of a span. */
  describe(span: Span): string {
    const pos = this.positionAt(span.start);
    return `line ${pos.line}, column ${pos.column}`;
  }

  slice(span: Span): string {
    return this.text.slice(span.start, span.end);
  }
}
