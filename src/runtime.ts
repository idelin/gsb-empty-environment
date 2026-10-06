import { Span } from './ast';
import { CallFrame, TemplateTypeError, UndefinedVariableError } from './errors';

/** A piece of rendered output, tagged with the template source range that produced it. */
export interface Fragment {
  text: string;
  /** Provenance: the text run or `{{ ... }}` tag this fragment was rendered from. */
  span: Span;
}

export interface RenderResult {
  fragments: Fragment[];
  text: string;
}

/**
 * Lexical scopes as a frame stack. Index 0 is the root data object; each loop
 * iteration pushes one frame and pops it on exit, so loop bindings shadow
 * outer names during the loop and the outer binding is restored afterwards.
 */
export type Scopes = Record<string, unknown>[];

export interface RenderContext {
  fragments: Fragment[];
  callStack: CallFrame[];
}

/** Engine operations the runtime helpers need (implemented by TemplateEngine). */
export interface EngineHost {
  renderInclude(name: string, scopes: Scopes, ctx: RenderContext, callSite: Span): void;
}

const hasOwn = (obj: object, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(obj, key);

/**
 * Resolve a dotted path against the scope chain, innermost frame first.
 * Throws UndefinedVariableError (with span + call stack) when any segment is missing.
 */
export function resolve(
  scopes: Scopes,
  path: string[],
  span: Span,
  ctx: RenderContext,
): unknown {
  const head = path[0];
  let value: unknown;
  let found = false;
  for (let i = scopes.length - 1; i >= 0; i--) {
    if (hasOwn(scopes[i], head)) {
      value = scopes[i][head];
      found = true;
      break;
    }
  }
  if (!found || value === undefined) {
    throw new UndefinedVariableError(path.join('.'), span, ctx.callStack);
  }
  for (let i = 1; i < path.length; i++) {
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) {
      throw new UndefinedVariableError(path.join('.'), span, ctx.callStack);
    }
    const key = path[i];
    if (!hasOwn(value as object, key)) {
      throw new UndefinedVariableError(path.join('.'), span, ctx.callStack);
    }
    value = (value as Record<string, unknown>)[key];
    if (value === undefined) {
      throw new UndefinedVariableError(path.join('.'), span, ctx.callStack);
    }
  }
  return value;
}

/** Convert an interpolated value to output text. `null` renders as empty. */
export function stringify(value: unknown): string {
  if (value === null) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

/** Condition truthiness: standard JS semantics. */
export function truthy(value: unknown): boolean {
  return Boolean(value);
}

/**
 * Iterate an array value, invoking `body(item, index)` per element.
 * Empty arrays simply produce no iterations. Non-arrays raise a type error
 * carrying the tag's span and the current call stack.
 */
export function each(
  value: unknown,
  span: Span,
  ctx: RenderContext,
  body: (item: unknown, index: number) => void,
): void {
  if (!Array.isArray(value)) {
    throw new TemplateTypeError(
      `'{{#each}}' expects an array, got ${value === null ? 'null' : typeof value}`,
      span,
      ctx.callStack,
    );
  }
  for (let i = 0; i < value.length; i++) {
    body(value[i], i);
  }
}

/** Render an included template, recording the call site on the call stack. */
export function include(
  engine: EngineHost,
  ctx: RenderContext,
  scopes: Scopes,
  name: string,
  callSite: Span,
): void {
  engine.renderInclude(name, scopes, ctx, callSite);
}

/** Shared runtime table handed to both the interpreter and compiled functions. */
export const rt = { resolve, stringify, truthy, each, include };
export type Runtime = typeof rt;
