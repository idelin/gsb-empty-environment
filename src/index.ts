import type { Node } from "./ast.ts";
import { Interpreter, type Fragment } from "./interpreter.ts";
import { parse } from "./parser.ts";
import { Source } from "./source.ts";

export { parse } from "./parser.ts";
export { Source } from "./source.ts";
export { Interpreter, type Fragment } from "./interpreter.ts";
export { TemplateRenderError, TemplateSyntaxError, type RenderFrame } from "./errors.ts";
export type { Expr, Node, Span } from "./ast.ts";

/** Parses a template string into an AST. */
export function parseTemplate(template: string): Node[] {
  return parse(new Source(template));
}

/** Renders a template to a plain string. */
export function renderTemplate(template: string, data: Record<string, unknown>): string {
  return renderFragments(template, data)
    .map((f) => f.text)
    .join("");
}

/**
 * Renders a template to output fragments. Every fragment keeps the span of
 * the template node that produced it, so output can be traced back to its
 * source position.
 */
export function renderFragments(template: string, data: Record<string, unknown>): Fragment[] {
  const source = new Source(template);
  const ast = parse(source);
  return new Interpreter(source).render(ast, data);
}
