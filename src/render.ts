import { formatPath, deepClone, type JsonValue, type PathSeg } from "./json.ts";
import { Store, type DataNode, type ChangeSet, type NodeId } from "./store.ts";
import type { Expr } from "./expr.ts";
import type { TNode, TFor, TIf, TString, TObject, MissingPolicy } from "./template.ts";
import { FnError, truthy, type FnImpl } from "./functions.ts";

export const MISSING: unique symbol = Symbol("missing");
export type MissingT = typeof MISSING;

export class TemplateError extends Error {
  readonly tpath: string | null;
  readonly ipath: string | null;
  constructor(msg: string, tpath: string | null = null, ipath: string | null = null) {
    const loc = [tpath ? `template ${tpath || "/"}` : null, ipath ? `input ${ipath}` : null]
      .filter(Boolean)
      .join(", ");
    super(loc ? `${msg} (${loc})` : msg);
    this.tpath = tpath;
    this.ipath = ipath;
  }
}

export class MissingError extends TemplateError {}

export class BudgetError extends Error {}

export interface Budget {
  expansionsLeft: number;
  outputLeft: number;
  maxDepth: number;
}

export interface RenderConfig {
  onMissing: MissingPolicy;
  functions: Record<string, FnImpl>;
}

export interface Ctx {
  store: Store;
  cfg: RenderConfig;
  budget: Budget;
  dirty: ChangeSet | null;
}

export interface Scope {
  vars: Map<string, DataNode | number>;
}

interface Rec {
  valueDeps: Set<NodeId>;
  structDeps: Set<NodeId>;
  deepDeps: Set<NodeId>;
  inputs: Map<NodeId, DataNode>;
  missingInputs: string[];
}

function newRec(): Rec {
  return {
    valueDeps: new Set(),
    structDeps: new Set(),
    deepDeps: new Set(),
    inputs: new Map(),
    missingInputs: [],
  };
}

export type FragShape = "value" | "object" | "array" | "passthrough";

export interface Fragment {
  tmpl: TNode;
  scope: Scope;
  depth: number;
  shape: FragShape;
  value: JsonValue;
  omitted: boolean;
  valueDeps: Set<NodeId>;
  structDeps: Set<NodeId>;
  deepDeps: Set<NodeId>;
  inputNodes: DataNode[];
  missingInputs: string[];
  children: Fragment[] | Map<string, Fragment> | null;
  itemId: NodeId | null;
  itemIndex: number;
  taken: boolean;
}

function spendExpansion(ctx: Ctx, what: string): void {
  if (--ctx.budget.expansionsLeft < 0) {
    throw new BudgetError(`expansion budget exceeded (shared across loops and function calls) while ${what}`);
  }
}

function spendOutput(ctx: Ctx): void {
  if (--ctx.budget.outputLeft < 0) {
    throw new BudgetError("output node budget exceeded (output explosion guard)");
  }
}

function mergeDepsInto(f: Fragment, c: Fragment): void {
  for (const id of c.valueDeps) f.valueDeps.add(id);
  for (const id of c.structDeps) f.structDeps.add(id);
  for (const id of c.deepDeps) f.deepDeps.add(id);
}

function makeFragment(
  tmpl: TNode,
  scope: Scope,
  depth: number,
  shape: FragShape,
  value: JsonValue,
  omitted: boolean,
  rec: Rec,
  children: Fragment[] | Map<string, Fragment> | null,
): Fragment {
  const f: Fragment = {
    tmpl,
    scope,
    depth,
    shape,
    value,
    omitted,
    valueDeps: rec.valueDeps,
    structDeps: rec.structDeps,
    deepDeps: rec.deepDeps,
    inputNodes: [...rec.inputs.values()],
    missingInputs: rec.missingInputs,
    children,
    itemId: null,
    itemIndex: -1,
    taken: false,
  };
  if (children instanceof Map) {
    for (const c of children.values()) mergeDepsInto(f, c);
  } else if (Array.isArray(children)) {
    for (const c of children) mergeDepsInto(f, c);
  }
  return f;
}

function resolvePath(
  segs: PathSeg[],
  scope: Scope,
  ctx: Ctx,
  rec: Rec,
  tpath: string,
): DataNode | number | MissingT {
  let node: DataNode;
  let base: PathSeg[];
  let start = 0;
  const first = segs[0];
  if (typeof first === "string" && first.startsWith("@")) {
    const v = scope.vars.get(first.slice(1));
    if (v === undefined) throw new TemplateError(`unknown loop variable ${first}`, tpath);
    if (typeof v === "number") {
      if (segs.length > 1) throw new TemplateError(`cannot index into numeric variable ${first}`, tpath);
      return v;
    }
    node = v;
    base = ctx.store.pathOf(v);
    start = 1;
  } else {
    node = ctx.store.root;
    base = [];
  }
  rec.valueDeps.add(node.id);
  const walked = base.slice();
  for (let k = start; k < segs.length; k++) {
    if (node.kind !== "value") rec.structDeps.add(node.id);
    const nxt = ctx.store.child(node, segs[k]);
    if (!nxt) {
      rec.missingInputs.push(formatPath([...walked, segs[k]]));
      return MISSING;
    }
    node = nxt;
    rec.valueDeps.add(node.id);
    walked.push(segs[k]);
  }
  rec.inputs.set(node.id, node);
  return node;
}

function evalExpr(
  e: Expr,
  scope: Scope,
  ctx: Ctx,
  rec: Rec,
  tpath: string,
): JsonValue | MissingT {
  if (e.t === "lit") return deepClone(e.v);
  if (e.t === "path") {
    const r = resolvePath(e.segs, scope, ctx, rec, tpath);
    if (r === MISSING) return MISSING;
    if (typeof r === "number") return r;
    if (r.kind === "value") return r.value;
    rec.deepDeps.add(r.id);
    return ctx.store.materialize(r);
  }
  spendExpansion(ctx, `calling ${e.fn}()`);
  const fn = e.fn;
  if (fn === "default") {
    if (e.args.length !== 2) throw new TemplateError("default() expects exactly 2 arguments", tpath);
    const a = evalExpr(e.args[0], scope, ctx, rec, tpath);
    return a === MISSING ? evalExpr(e.args[1], scope, ctx, rec, tpath) : a;
  }
  if (fn === "defined") {
    if (e.args.length !== 1) throw new TemplateError("defined() expects exactly 1 argument", tpath);
    return evalExpr(e.args[0], scope, ctx, rec, tpath) !== MISSING;
  }
  if (fn === "and" || fn === "or") {
    let last: JsonValue | MissingT = fn === "and" ? true : false;
    for (const arg of e.args) {
      const v = evalExpr(arg, scope, ctx, rec, tpath);
      if (v === MISSING) return MISSING;
      last = v;
      if (fn === "and" && !truthy(v)) return v;
      if (fn === "or" && truthy(v)) return v;
    }
    return last;
  }
  const impl = ctx.cfg.functions[fn];
  if (!impl) throw new TemplateError(`unknown function '${fn}' (not in whitelist)`, tpath);
  const args: JsonValue[] = [];
  for (const a of e.args) {
    const v = evalExpr(a, scope, ctx, rec, tpath);
    if (v === MISSING) return MISSING;
    args.push(v);
  }
  try {
    return impl(args);
  } catch (err) {
    if (err instanceof FnError) throw new TemplateError(err.message, tpath);
    throw err;
  }
}

function lastMissingInput(rec: Rec): string | null {
  return rec.missingInputs.length > 0 ? rec.missingInputs[rec.missingInputs.length - 1] : null;
}

interface MissingOutcome {
  omitted: boolean;
  value: JsonValue;
}

function applyMissing(policy: MissingPolicy, tpath: string, rec: Rec, raw: string | null): MissingOutcome {
  const ipath = lastMissingInput(rec);
  switch (policy) {
    case "error":
      throw new MissingError("input path is missing", tpath, ipath);
    case "null":
      return { omitted: false, value: null };
    case "empty":
      return { omitted: false, value: "" };
    case "omit":
      return { omitted: true, value: null };
    case "keep":
      return { omitted: false, value: raw !== null ? "${" + raw + "}" : "" };
  }
}

function toText(v: JsonValue): string {
  if (v === null) return "null";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

export function renderNode(tmpl: TNode, scope: Scope, ctx: Ctx, depth: number): Fragment {
  if (depth > ctx.budget.maxDepth) {
    throw new BudgetError(`template nesting depth exceeded (max ${ctx.budget.maxDepth}) at template ${tmpl.tpath || "/"}`);
  }
  spendOutput(ctx);
  const rec = newRec();
  switch (tmpl.kind) {
    case "value":
      return makeFragment(tmpl, scope, depth, "value", deepClone(tmpl.v), false, rec, null);
    case "string":
      return renderString(tmpl, scope, ctx, depth, rec);
    case "object": {
      const children = new Map<string, Fragment>();
      const out: { [k: string]: JsonValue } = {};
      for (const en of tmpl.entries) {
        const c = renderNode(en.val, scope, ctx, depth + 1);
        children.set(en.key, c);
        if (!c.omitted) out[en.key] = c.value;
      }
      return makeFragment(tmpl, scope, depth, "object", out, false, rec, children);
    }
    case "array": {
      const children: Fragment[] = [];
      const out: JsonValue[] = [];
      for (const item of tmpl.items) {
        const c = renderNode(item, scope, ctx, depth + 1);
        children.push(c);
        if (!c.omitted) out.push(c.value);
      }
      return makeFragment(tmpl, scope, depth, "array", out, false, rec, children);
    }
    case "for":
      return renderFor(tmpl, scope, ctx, depth, rec);
    case "if":
      return renderIf(tmpl, scope, ctx, depth, rec);
    case "get": {
      const v = evalExpr(tmpl.expr, scope, ctx, rec, tmpl.tpath);
      if (v === MISSING) {
        const oc = applyMissing(tmpl.missing ?? ctx.cfg.onMissing, tmpl.tpath, rec, tmpl.raw);
        return makeFragment(tmpl, scope, depth, "value", oc.value, oc.omitted, rec, null);
      }
      return makeFragment(tmpl, scope, depth, "value", v, false, rec, null);
    }
  }
}

function renderString(tmpl: TString, scope: Scope, ctx: Ctx, depth: number, rec: Rec): Fragment {
  if (tmpl.single) {
    const part = tmpl.parts[0];
    if ("e" in part) {
      const v = evalExpr(part.e, scope, ctx, rec, tmpl.tpath);
      if (v === MISSING) {
        const oc = applyMissing(ctx.cfg.onMissing, tmpl.tpath, rec, part.raw);
        return makeFragment(tmpl, scope, depth, "value", oc.value, oc.omitted, rec, null);
      }
      return makeFragment(tmpl, scope, depth, "value", v, false, rec, null);
    }
    return makeFragment(tmpl, scope, depth, "value", part.s, false, rec, null);
  }
  let out = "";
  for (const part of tmpl.parts) {
    if ("s" in part) {
      out += part.s;
      continue;
    }
    const v = evalExpr(part.e, scope, ctx, rec, tmpl.tpath);
    if (v === MISSING) {
      const pol = ctx.cfg.onMissing;
      if (pol === "error") throw new MissingError("input path is missing", tmpl.tpath, lastMissingInput(rec));
      if (pol === "keep") out += "${" + part.raw + "}";
      else if (pol === "null") out += "null";
      continue;
    }
    out += toText(v);
  }
  return makeFragment(tmpl, scope, depth, "value", out, false, rec, null);
}

function forMissing(tmpl: TFor, scope: Scope, ctx: Ctx, depth: number, rec: Rec): Fragment {
  const pol = tmpl.missing ?? ctx.cfg.onMissing;
  if (pol === "error") throw new MissingError("input path is missing", tmpl.tpath, lastMissingInput(rec));
  if (pol === "omit") return makeFragment(tmpl, scope, depth, "array", null, true, rec, []);
  return makeFragment(tmpl, scope, depth, "array", [], false, rec, []);
}

function renderForChild(tmpl: TFor, scope: Scope, ctx: Ctx, depth: number, itemNode: DataNode, idx: number): Fragment {
  spendExpansion(ctx, `iterating ${tmpl.tpath || "/"}`);
  const childScope: Scope = { vars: new Map(scope.vars) };
  childScope.vars.set(tmpl.as, itemNode);
  if (tmpl.indexAs !== null) childScope.vars.set(tmpl.indexAs, idx);
  const c = renderNode(tmpl.body, childScope, ctx, depth + 1);
  c.itemId = itemNode.id;
  c.itemIndex = idx;
  return c;
}

function renderFor(tmpl: TFor, scope: Scope, ctx: Ctx, depth: number, rec: Rec): Fragment {
  const target = resolvePath((tmpl.expr as { segs: PathSeg[] }).segs, scope, ctx, rec, tmpl.tpath);
  if (target === MISSING) return forMissing(tmpl, scope, ctx, depth, rec);
  if (typeof target === "number" || target.kind !== "array") {
    throw new TemplateError("$for expects an array", tmpl.tpath);
  }
  rec.structDeps.add(target.id);
  const items = target.children as DataNode[];
  const children: Fragment[] = [];
  const out: JsonValue[] = [];
  items.forEach((itemNode, idx) => {
    const c = renderForChild(tmpl, scope, ctx, depth, itemNode, idx);
    children.push(c);
    if (!c.omitted) out.push(c.value);
  });
  return makeFragment(tmpl, scope, depth, "array", out, false, rec, children);
}

function evalCond(tmpl: TIf, scope: Scope, ctx: Ctx, rec: Rec): boolean {
  const c = evalExpr(tmpl.cond, scope, ctx, rec, tmpl.tpath);
  if (c === MISSING) {
    const pol = tmpl.missing ?? ctx.cfg.onMissing;
    if (pol === "error") throw new MissingError("input path is missing", tmpl.tpath, lastMissingInput(rec));
    return false;
  }
  return truthy(c);
}

function renderIf(tmpl: TIf, scope: Scope, ctx: Ctx, depth: number, rec: Rec): Fragment {
  const taken = evalCond(tmpl, scope, ctx, rec);
  const branch = taken ? tmpl.then : tmpl.otherwise;
  if (branch === null) {
    return makeFragment(tmpl, scope, depth, "passthrough", null, true, rec, []);
  }
  const child = renderNode(branch, scope, ctx, depth + 1);
  const f = makeFragment(tmpl, scope, depth, "passthrough", child.value, child.omitted, rec, [child]);
  f.taken = taken;
  return f;
}

function isDirty(f: Fragment, cs: ChangeSet, store: Store): boolean {
  for (const id of f.valueDeps) if (cs.valueDirty.has(id)) return true;
  for (const id of f.structDeps) if (cs.structDirty.has(id)) return true;
  if (f.deepDeps.size > 0) {
    const check = (id: NodeId): boolean => {
      let n = store.byId.get(id);
      while (n) {
        if (f.deepDeps.has(n.id)) return true;
        n = n.parent as DataNode;
      }
      return false;
    };
    for (const id of cs.valueDirty) if (check(id)) return true;
    for (const id of cs.structDirty) if (check(id)) return true;
  }
  return false;
}

export function refresh(frag: Fragment, ctx: Ctx): Fragment {
  if (ctx.dirty && !isDirty(frag, ctx.dirty, ctx.store)) return frag;
  const tmpl = frag.tmpl;
  switch (tmpl.kind) {
    case "object": {
      const oldChildren = frag.children as Map<string, Fragment>;
      const children = new Map<string, Fragment>();
      let changed = false;
      for (const en of (tmpl as TObject).entries) {
        const old = oldChildren.get(en.key) as Fragment;
        const nc = refresh(old, ctx);
        children.set(en.key, nc);
        if (nc !== old) changed = true;
      }
      if (!changed) return frag;
      const out: { [k: string]: JsonValue } = {};
      for (const [k, c] of children) if (!c.omitted) out[k] = c.value;
      return makeFragment(tmpl, frag.scope, frag.depth, "object", out, false, newRec(), children);
    }
    case "array": {
      const oldChildren = frag.children as Fragment[];
      const children: Fragment[] = [];
      let changed = false;
      for (const old of oldChildren) {
        const nc = refresh(old, ctx);
        children.push(nc);
        if (nc !== old) changed = true;
      }
      if (!changed) return frag;
      const out: JsonValue[] = [];
      for (const c of children) if (!c.omitted) out.push(c.value);
      return makeFragment(tmpl, frag.scope, frag.depth, "array", out, false, newRec(), children);
    }
    case "for":
      return refreshFor(frag, tmpl, ctx);
    case "if":
      return refreshIf(frag, tmpl as TIf, ctx);
    default:
      return renderNode(tmpl, frag.scope, ctx, frag.depth);
  }
}

function refreshFor(frag: Fragment, tmpl: TFor, ctx: Ctx): Fragment {
  const rec = newRec();
  const target = resolvePath((tmpl.expr as { segs: PathSeg[] }).segs, frag.scope, ctx, rec, tmpl.tpath);
  if (target === MISSING || typeof target === "number" || target.kind !== "array") {
    return renderNode(tmpl, frag.scope, ctx, frag.depth);
  }
  rec.structDeps.add(target.id);
  const items = target.children as DataNode[];
  const oldChildren = (frag.children ?? []) as Fragment[];
  const byId = new Map<NodeId, Fragment>();
  for (const c of oldChildren) if (c.itemId !== null) byId.set(c.itemId, c);
  const children: Fragment[] = [];
  const out: JsonValue[] = [];
  items.forEach((itemNode, idx) => {
    const old = byId.get(itemNode.id);
    let c: Fragment;
    if (old !== undefined && (!tmpl.usesIndex || old.itemIndex === idx)) {
      const kept = refresh(old, ctx);
      if (kept !== old) {
        kept.itemId = itemNode.id;
        kept.itemIndex = idx;
      }
      c = kept;
    } else {
      c = renderForChild(tmpl, frag.scope, ctx, frag.depth, itemNode, idx);
    }
    children.push(c);
    if (!c.omitted) out.push(c.value);
  });
  return makeFragment(tmpl, frag.scope, frag.depth, "array", out, false, rec, children);
}

function refreshIf(frag: Fragment, tmpl: TIf, ctx: Ctx): Fragment {
  const rec = newRec();
  const taken = evalCond(tmpl, frag.scope, ctx, rec);
  const branch = taken ? tmpl.then : tmpl.otherwise;
  if (branch === null) {
    return makeFragment(tmpl, frag.scope, frag.depth, "passthrough", null, true, rec, []);
  }
  const oldChild = frag.children && frag.children.length === 1 ? (frag.children as Fragment[])[0] : null;
  let child: Fragment;
  if (oldChild !== null && taken === frag.taken && oldChild.tmpl === branch) {
    child = refresh(oldChild, ctx);
  } else {
    child = renderNode(branch, frag.scope, ctx, frag.depth + 1);
  }
  const f = makeFragment(tmpl, frag.scope, frag.depth, "passthrough", child.value, child.omitted, rec, [child]);
  f.taken = taken;
  return f;
}

export interface ProvEntry {
  template: string;
  inputs: string[];
}

export interface CollectResult {
  output: JsonValue;
  provenance: Record<string, ProvEntry>;
}

export function collect(root: Fragment, store: Store): CollectResult {
  const provenance: Record<string, ProvEntry> = {};
  const ownInputs = (f: Fragment): string[] => {
    const paths = f.inputNodes.map((n) => formatPath(store.pathOf(n)));
    return [...paths, ...f.missingInputs];
  };
  const walk = (f: Fragment, path: string): JsonValue => {
    const key = path === "" ? "/" : path;
    if (f.shape === "passthrough") {
      const child = (f.children as Fragment[])[0];
      const v = walk(child, path);
      const entry = provenance[key];
      entry.inputs = [...new Set([...ownInputs(f), ...entry.inputs])];
      return v;
    }
    provenance[key] = { template: f.tmpl.tpath || "/", inputs: ownInputs(f) };
    if (f.shape === "object") {
      const out: { [k: string]: JsonValue } = {};
      for (const [k, c] of f.children as Map<string, Fragment>) {
        if (c.omitted) continue;
        out[k] = walk(c, `${path}/${k}`);
      }
      return out;
    }
    if (f.shape === "array") {
      const out: JsonValue[] = [];
      for (const c of f.children as Fragment[]) {
        if (c.omitted) continue;
        out.push(walk(c, `${path}/${out.length}`));
      }
      return out;
    }
    return f.value;
  };
  const output = root.omitted ? null : walk(root, "");
  return { output, provenance };
}
