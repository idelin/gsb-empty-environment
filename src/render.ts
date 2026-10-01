import type { JsonValue } from "./json.ts";
import { pointerOf } from "./json.ts";
import { TemplateError } from "./errors.ts";
import type { Expr } from "./expr.ts";
import type { TNode } from "./template.ts";
import { isTruthy } from "./functions.ts";
import type { FnImpl } from "./functions.ts";
import { isArr } from "./store.ts";
import type { JNode, Store, SymSeg } from "./store.ts";

/** A read dependency: symbolic path + the exact node reference seen at render time. */
export interface Dep {
  sym: SymSeg[];
  ref: JNode;
}

/** Provenance for one output node (relative `out` path, template id, input reads). */
export interface Prov {
  out: string[];
  template: string;
  input: SymSeg[][];
}

export interface RenderOut {
  value: JsonValue;
  prov: Prov[];
  deps: Dep[];
}

/** Cached expansion of one loop iteration (one entity through one loop body). */
export interface Fragment {
  value: JsonValue;
  prov: Prov[];
  deps: Dep[];
}

export interface Scope {
  name: string;
  sym: SymSeg[];
  concrete: string[];
  node: JNode;
}

export interface RenderStats {
  hits: number;
  misses: number;
  expansions: number;
}

export interface RenderContext {
  store: Store;
  scopes: Scope[];
  onMissing: "empty" | "null" | "error";
  functions: ReadonlyMap<string, FnImpl>;
  partials: ReadonlyMap<string, TNode>;
  cache: ReadonlyMap<string, Fragment> | null;
  nextCache: Map<string, Fragment> | null;
  /**
   * Shared expansion budget across ALL loops/refs in this render. Kept behind
   * an object reference so child contexts (which spread-copy the context)
   * decrement the SAME counter.
   */
  budget: { expansionsLeft: number };
  maxDepth: number;
  stats: RenderStats;
}

interface ExprResult {
  value: JsonValue | undefined;
  missing: boolean;
  deps: Dep[];
  input: SymSeg[];
  inputPath: string | undefined;
}

function resolveHead(
  path: string[],
  ctx: RenderContext,
): { found: boolean; value: JNode; sym: SymSeg[]; concrete: string[] } {
  if (path[0] === "$root") {
    return ctx.store.resolvePath(path.slice(1));
  }
  for (let i = ctx.scopes.length - 1; i >= 0; i--) {
    const scope = ctx.scopes[i];
    if (scope.name === path[0]) {
      return ctx.store.walk(scope.node, scope.sym, scope.concrete, path.slice(1));
    }
  }
  return ctx.store.resolvePath(path);
}

function evalExpr(expr: Expr, templateId: string, ctx: RenderContext): ExprResult {
  const head = resolveHead(expr.path, ctx);
  const deps: Dep[] = [{ sym: head.sym, ref: head.value }];
  const inputPath = head.found ? pointerOf(head.concrete) : undefined;
  let value: JsonValue | undefined = head.found ? ctx.store.toJson(head.value) : undefined;
  let missing = !head.found;
  for (const pipe of expr.pipes) {
    const fn = ctx.functions.get(pipe.name);
    if (fn === undefined) {
      throw new TemplateError(`unknown function "${pipe.name}" (not in whitelist)`, templateId, inputPath);
    }
    if (missing && pipe.name !== "default") {
      if (ctx.onMissing === "error") {
        throw new TemplateError(`missing value at "${expr.path.join(".")}"`, templateId, inputPath);
      }
      value = ctx.onMissing === "empty" ? "" : null;
    }
    value = fn(value, missing, pipe.args, { templatePath: templateId, inputPath });
    if (pipe.name === "default") missing = false;
  }
  return { value, missing, deps, input: head.sym, inputPath };
}

function missingError(expr: Expr, templateId: string, inputPath: string | undefined): TemplateError {
  return new TemplateError(`missing value at "${expr.path.join(".")}"`, templateId, inputPath);
}

function stringifyText(value: JsonValue | undefined, missing: boolean, templateId: string, ctx: RenderContext): string {
  if (missing) {
    if (ctx.onMissing === "error") {
      throw new TemplateError("missing value in string interpolation", templateId);
    }
    return "";
  }
  if (value === null || value === undefined) return "null";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function depsValid(deps: Dep[], store: Store): boolean {
  for (const dep of deps) {
    const r = store.resolveSym(dep.sym);
    if (!r.found || r.value !== dep.ref) return false;
  }
  return true;
}

export function render(node: TNode, ctx: RenderContext, depth: number): RenderOut {
  if (depth > ctx.maxDepth) {
    throw new TemplateError(`max template depth ${ctx.maxDepth} exceeded (recursive template?)`, node.id);
  }
  switch (node.kind) {
    case "literal":
      return { value: node.value, prov: [{ out: [], template: node.id, input: [] }], deps: [] };
    case "text":
      return renderText(node, ctx);
    case "value":
      return renderValue(node, ctx);
    case "object":
      return renderObject(node, ctx, depth);
    case "array":
      return renderArray(node, ctx, depth);
    case "if":
      return renderIf(node, ctx, depth);
    case "for":
      return renderFor(node, ctx, depth);
    case "ref":
      return renderRef(node, ctx, depth);
  }
}

function renderText(node: TNode & { kind: "text" }, ctx: RenderContext): RenderOut {
  let text = "";
  const deps: Dep[] = [];
  const inputs: SymSeg[][] = [];
  for (const part of node.parts) {
    if (part.kind === "str") {
      text += part.text;
      continue;
    }
    const r = evalExpr(part.expr, node.id, ctx);
    deps.push(...r.deps);
    inputs.push(r.input);
    text += stringifyText(r.value, r.missing, node.id, ctx);
  }
  return { value: text, prov: [{ out: [], template: node.id, input: inputs }], deps };
}

function renderValue(node: TNode & { kind: "value" }, ctx: RenderContext): RenderOut {
  const r = evalExpr(node.expr, node.id, ctx);
  let value: JsonValue;
  if (r.missing) {
    if (ctx.onMissing === "error") throw missingError(node.expr, node.id, r.inputPath);
    value = ctx.onMissing === "empty" ? "" : null;
  } else {
    value = r.value === undefined ? null : r.value;
  }
  return { value, prov: [{ out: [], template: node.id, input: [r.input] }], deps: r.deps };
}

function renderObject(node: TNode & { kind: "object" }, ctx: RenderContext, depth: number): RenderOut {
  const out: { [key: string]: JsonValue } = {};
  const prov: Prov[] = [{ out: [], template: node.id, input: [] }];
  const deps: Dep[] = [];
  for (const [key, child] of node.entries) {
    const r = render(child, ctx, depth + 1);
    out[key] = r.value;
    for (const p of r.prov) prov.push({ out: [key, ...p.out], template: p.template, input: p.input });
    deps.push(...r.deps);
  }
  return { value: out, prov, deps };
}

function renderArray(node: TNode & { kind: "array" }, ctx: RenderContext, depth: number): RenderOut {
  const items: JsonValue[] = [];
  const prov: Prov[] = [{ out: [], template: node.id, input: [] }];
  const deps: Dep[] = [];
  for (let i = 0; i < node.items.length; i++) {
    const r = render(node.items[i], ctx, depth + 1);
    items.push(r.value);
    for (const p of r.prov) prov.push({ out: [String(i), ...p.out], template: p.template, input: p.input });
    deps.push(...r.deps);
  }
  return { value: items, prov, deps };
}

function renderIf(node: TNode & { kind: "if" }, ctx: RenderContext, depth: number): RenderOut {
  const r = evalExpr(node.expr, node.id, ctx);
  if (r.missing && ctx.onMissing === "error") throw missingError(node.expr, node.id, r.inputPath);
  const cond = r.missing ? false : isTruthy(r.value);
  const branch = cond ? node.then : node.else;
  if (branch === null) {
    return { value: null, prov: [{ out: [], template: node.id, input: [r.input] }], deps: r.deps };
  }
  const out = render(branch, ctx, depth + 1);
  return { value: out.value, prov: out.prov, deps: [...r.deps, ...out.deps] };
}

function renderFor(node: TNode & { kind: "for" }, ctx: RenderContext, depth: number): RenderOut {
  const head = resolveHead(node.path, ctx);
  const deps: Dep[] = [{ sym: head.sym, ref: head.value }];
  const prov: Prov[] = [{ out: [], template: node.id, input: [head.sym] }];
  let entries;
  if (!head.found || head.value === null) {
    if (ctx.onMissing === "error") {
      throw new TemplateError(`missing array at "${node.path.join(".")}"`, node.id, pointerOf(head.concrete));
    }
    entries = [];
  } else if (isArr(head.value)) {
    entries = head.value.entries;
  } else {
    throw new TemplateError(
      `$for target "${node.path.join(".")}" is not an array`,
      node.id,
      pointerOf(head.concrete),
    );
  }
  const items: JsonValue[] = [];
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    // Loops exposing $index are position-sensitive: the index joins the cache key.
    const key =
      node.indexAs === null ? `${node.id}#${entry.eid}` : `${node.id}#${entry.eid}@${i}`;
    let frag: Fragment | undefined = ctx.cache === null ? undefined : ctx.cache.get(key);
    if (frag !== undefined && !depsValid(frag.deps, ctx.store)) frag = undefined;
    let childOut: RenderOut;
    if (frag !== undefined) {
      ctx.stats.hits++;
      childOut = frag;
    } else {
      ctx.stats.misses++;
      ctx.budget.expansionsLeft--;
      ctx.stats.expansions++;
      if (ctx.budget.expansionsLeft < 0) {
        throw new TemplateError("expansion budget exceeded", node.id, pointerOf(head.concrete));
      }
      const scopes = ctx.scopes.concat([
        {
          name: node.as,
          sym: [...head.sym, { e: entry.eid }],
          concrete: [...head.concrete, String(i)],
          node: entry.value,
        },
      ]);
      if (node.indexAs !== null) {
        scopes.push({ name: node.indexAs, sym: [], concrete: [], node: i });
      }
      childOut = render(node.body, { ...ctx, scopes }, depth + 1);
      frag = { value: childOut.value, prov: childOut.prov, deps: childOut.deps };
    }
    if (ctx.nextCache !== null) ctx.nextCache.set(key, frag);
    items.push(childOut.value);
    for (const p of childOut.prov) {
      prov.push({ out: [String(i), ...p.out], template: p.template, input: p.input });
    }
    deps.push(...childOut.deps);
  }
  return { value: items, prov, deps };
}

function renderRef(node: TNode & { kind: "ref" }, ctx: RenderContext, depth: number): RenderOut {
  const target = ctx.partials.get(node.name);
  if (target === undefined) {
    throw new TemplateError(`unknown partial "${node.name}"`, node.id);
  }
  ctx.budget.expansionsLeft--;
  ctx.stats.expansions++;
  if (ctx.budget.expansionsLeft < 0) {
    throw new TemplateError("expansion budget exceeded", node.id);
  }
  const r = render(target, ctx, depth + 1);
  return {
    value: r.value,
    prov: [{ out: [], template: node.id, input: [] }, ...r.prov],
    deps: r.deps,
  };
}
