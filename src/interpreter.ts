import type { Expr, Node, Span } from "./ast.ts";
import { TemplateRenderError, type RenderFrame } from "./errors.ts";
import type { Source } from "./source.ts";

/** A chunk of rendered output, traced back to the template node that made it. */
export interface Fragment {
  text: string;
  /** What produced this fragment: a literal text run or a `{{ ... }}` output. */
  origin: "text" | "output";
  /** Span of the source node in the template. */
  span: Span;
}

/** Lexical scope: a variable map linked to its parent scope. */
class Env {
  private readonly vars = new Map<string, unknown>();
  private readonly parent: Env | null;

  constructor(parent: Env | null = null) {
    this.parent = parent;
  }

  static root(data: Record<string, unknown>): Env {
    const env = new Env(null);
    for (const key of Object.keys(data)) env.vars.set(key, data[key]);
    return env;
  }

  has(name: string): boolean {
    if (this.vars.has(name)) return true;
    return this.parent !== null && this.parent.has(name);
  }

  get(name: string): unknown {
    if (this.vars.has(name)) return this.vars.get(name);
    if (this.parent !== null) return this.parent.get(name);
    throw new Error(`internal: unbound variable '${name}'`);
  }

  set(name: string, value: unknown): void {
    this.vars.set(name, value);
  }
}

/**
 * Direct tree-walking interpreter for the template AST.
 *
 * Semantics:
 * - `{% if %}` evaluates only the branch that is taken; expressions in
 *   unselected branches are never touched.
 * - `{% for x in xs %}` binds `x` in a fresh child scope per iteration, so
 *   it shadows any outer `x`; the outer binding is intact after the loop.
 * - Reading an undefined variable throws TemplateRenderError carrying the
 *   template position and the template call stack.
 */
export class Interpreter {
  private readonly stack: RenderFrame[] = [];
  private readonly source: Source;

  constructor(source: Source) {
    this.source = source;
  }

  render(nodes: Node[], data: Record<string, unknown>): Fragment[] {
    const fragments: Fragment[] = [];
    this.evalNodes(nodes, Env.root(data), fragments);
    return fragments;
  }

  private evalNodes(nodes: Node[], env: Env, out: Fragment[]): void {
    for (const node of nodes) {
      switch (node.kind) {
        case "text":
          out.push({ text: node.text, origin: "text", span: node.span });
          break;
        case "output": {
          const value = this.evalExpr(node.expr, env);
          out.push({ text: stringify(value), origin: "output", span: node.span });
          break;
        }
        case "if":
          this.evalIf(node, env, out);
          break;
        case "for":
          this.evalFor(node, env, out);
          break;
      }
    }
  }

  private evalIf(node: Node & { kind: "if" }, env: Env, out: Fragment[]): void {
    for (const branch of node.branches) {
      // Only the condition is evaluated; bodies of unselected branches
      // (and conditions after a matched branch) are never evaluated.
      if (isTruthy(this.evalExpr(branch.cond, env))) {
        this.withFrame(`{% if %}`, branch.span, () => this.evalNodes(branch.body, env, out));
        return;
      }
    }
    if (node.elseBody !== null) {
      this.withFrame(`{% else %}`, node.span, () => this.evalNodes(node.elseBody!, env, out));
    }
  }

  private evalFor(node: Node & { kind: "for" }, env: Env, out: Fragment[]): void {
    const iterable = this.evalExpr(node.iterable, env);
    if (!Array.isArray(iterable)) {
      throw this.error(
        `Cannot iterate over ${iterable === null ? "null" : typeof iterable} (expected an array)`,
        node.iterable.span,
      );
    }
    // Empty loop: body is never evaluated, no output is produced.
    for (let i = 0; i < iterable.length; i++) {
      // Fresh child scope per iteration: the loop variable shadows any
      // outer binding, which is restored when the scope is dropped.
      const child = new Env(env);
      child.set(node.varName, iterable[i]);
      this.withFrame(`{% for ${node.varName} in ... %} (iteration ${i + 1})`, node.span, () =>
        this.evalNodes(node.body, child, out),
      );
    }
  }

  private evalExpr(expr: Expr, env: Env): unknown {
    if (expr.kind === "literal") return expr.value;

    const [head, ...rest] = expr.parts;
    if (!env.has(head)) {
      throw this.error(`Undefined variable '${head}'`, expr.span);
    }
    let value: unknown = env.get(head);
    for (const part of rest) {
      if (value === null || value === undefined || typeof value !== "object") {
        throw this.error(
          `Cannot read property '${part}' of ${value === null ? "null" : value === undefined ? "undefined" : typeof value}`,
          expr.span,
        );
      }
      if (!(part in value)) {
        throw this.error(`Undefined variable '${expr.parts.join(".")}'`, expr.span);
      }
      value = (value as Record<string, unknown>)[part];
    }
    if (value === undefined) {
      throw this.error(`Undefined variable '${expr.parts.join(".")}'`, expr.span);
    }
    return value;
  }

  private withFrame(label: string, span: Span, run: () => void): void {
    this.stack.push({ label, span });
    try {
      run();
    } finally {
      this.stack.pop();
    }
  }

  private error(message: string, span: Span): TemplateRenderError {
    // Innermost frame first, like a conventional call stack trace.
    return new TemplateRenderError(message, span, [...this.stack].reverse(), this.source);
  }
}

function isTruthy(value: unknown): boolean {
  if (value === null || value === undefined || value === false) return false;
  if (value === 0 || value === "") return false;
  if (Array.isArray(value) && value.length === 0) return false;
  return true;
}

function stringify(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
