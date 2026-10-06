import { Span } from './ast';

/** One entry of the template call stack: an include call site. */
export interface CallFrame {
  /** Template being entered (the include target). */
  template: string;
  /** Call site of the `{{> ...}}` tag in the including template. */
  span: Span;
}

export function formatSpan(span: Span): string {
  return `${span.template}:${span.start.line}:${span.start.col}`;
}

export class TemplateError extends Error {
  readonly span: Span | null;
  /** Snapshot of the include call stack at the moment the error was raised. */
  readonly callStack: readonly CallFrame[];

  constructor(message: string, span: Span | null, callStack: readonly CallFrame[]) {
    super(message);
    this.name = new.target.name;
    this.span = span;
    this.callStack = callStack.slice();
  }

  /** Human-readable rendering: message, template position, then the include stack. */
  format(): string {
    const lines = [`${this.name}: ${this.message}`];
    if (this.span) lines.push(`  at ${formatSpan(this.span)}`);
    for (let i = this.callStack.length - 1; i >= 0; i--) {
      const frame = this.callStack[i];
      lines.push(`  included from ${formatSpan(frame.span)} (-> ${frame.template})`);
    }
    return lines.join('\n');
  }
}

export class TemplateSyntaxError extends TemplateError {}

export class UnknownTemplateError extends TemplateError {}

export class UndefinedVariableError extends TemplateError {
  readonly path: string;
  constructor(path: string, span: Span, callStack: readonly CallFrame[]) {
    super(`variable '${path}' is not defined`, span, callStack);
    this.path = path;
  }
}

export class TemplateTypeError extends TemplateError {}
