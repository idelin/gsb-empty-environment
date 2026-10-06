import type { Span } from "./ast.ts";
import type { Source } from "./source.ts";

/** One frame of the template render stack (an enclosing construct). */
export interface RenderFrame {
  /** Human-readable label, e.g. `{% for user in users %}`. */
  label: string;
  span: Span;
}

/** Error thrown for template syntax problems (lexing/parsing). */
export class TemplateSyntaxError extends Error {
  readonly span: Span;

  constructor(message: string, span: Span, source: Source) {
    super(`${message} at ${source.describe(span)}`);
    this.name = "TemplateSyntaxError";
    this.span = span;
  }
}

/**
 * Error thrown while rendering. Carries the template source position of the
 * failing expression plus the template call stack (enclosing constructs).
 */
export class TemplateRenderError extends Error {
  readonly span: Span;
  readonly templateStack: RenderFrame[];

  constructor(message: string, span: Span, templateStack: RenderFrame[], source: Source) {
    const lines = [`${message} at ${source.describe(span)}`, "Template stack (most recent first):"];
    for (const frame of templateStack) {
      lines.push(`  at ${frame.label} (${source.describe(frame.span)})`);
    }
    super(lines.join("\n"));
    this.name = "TemplateRenderError";
    this.span = span;
    this.templateStack = templateStack;
  }
}
