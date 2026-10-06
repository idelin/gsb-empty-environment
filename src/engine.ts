import { Node, Span } from './ast';
import { compile, CompiledFn } from './compiler';
import { UnknownTemplateError } from './errors';
import { interpret } from './interpreter';
import { parse } from './parser';
import {
  EngineHost,
  RenderContext,
  RenderResult,
  rt,
  Scopes,
} from './runtime';

export type EngineMode = 'interpret' | 'compile';

/**
 * A set of named templates rendered with a shared data scope.
 *
 * Two execution engines are provided with identical observable behaviour:
 *  - `interpret`: direct AST tree-walking (the reference model);
 *  - `compile`:   AST -> JavaScript codegen.
 */
export class TemplateEngine implements EngineHost {
  private readonly asts = new Map<string, Node[]>();
  private readonly compiled = new Map<string, CompiledFn>();

  constructor(private readonly mode: EngineMode = 'interpret') {}

  addTemplate(name: string, source: string): this {
    this.asts.set(name, parse(name, source));
    this.compiled.delete(name);
    return this;
  }

  hasTemplate(name: string): boolean {
    return this.asts.has(name);
  }

  /** Render `name` against `data`, returning text plus provenance fragments. */
  render(name: string, data: Record<string, unknown>): RenderResult {
    const ctx: RenderContext = { fragments: [], callStack: [] };
    const scopes: Scopes = [data];
    this.renderTemplate(name, scopes, ctx, null);
    return {
      fragments: ctx.fragments,
      text: ctx.fragments.map((f) => f.text).join(''),
    };
  }

  /** EngineHost: render an included template, pushing a call-stack frame. */
  renderInclude(name: string, scopes: Scopes, ctx: RenderContext, callSite: Span): void {
    this.renderTemplate(name, scopes, ctx, callSite);
  }

  private renderTemplate(
    name: string,
    scopes: Scopes,
    ctx: RenderContext,
    callSite: Span | null,
  ): void {
    const ast = this.asts.get(name);
    if (!ast) {
      throw new UnknownTemplateError(`unknown template '${name}'`, callSite, ctx.callStack);
    }
    if (callSite) ctx.callStack.push({ template: name, span: callSite });
    try {
      if (this.mode === 'compile') {
        let fn = this.compiled.get(name);
        if (!fn) {
          fn = compile(ast);
          this.compiled.set(name, fn);
        }
        fn(rt, this, scopes, ctx);
      } else {
        interpret(ast, rt, this, scopes, ctx);
      }
    } finally {
      if (callSite) ctx.callStack.pop();
    }
  }
}
