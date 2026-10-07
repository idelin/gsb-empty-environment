// extract.ts — extract a subtree into a top-level function.

import { BindingId, Expr, NodeId, children, findNode, freshBindId, freshNodeId } from './core';
import { freeVars } from './analyze';

export class ExtractError extends Error {}

export interface Param {
  name: string; // display name of the new parameter (deduplicated)
  bind: BindingId; // fresh binding id of the new parameter
  capturedFrom: BindingId; // original binding id this parameter captures
}

export class SourceMap {
  readonly genToOrig = new Map<NodeId, NodeId>();
  readonly origToGen = new Map<NodeId, NodeId[]>();
  callSite?: NodeId; // root of the generated call
  target?: NodeId; // original subtree root that was extracted
  fnNode?: NodeId; // root of the generated function

  add(gen: NodeId, orig: NodeId): void {
    this.genToOrig.set(gen, orig);
    const arr = this.origToGen.get(orig) ?? [];
    arr.push(gen);
    this.origToGen.set(orig, arr);
  }
  originOf(gen: NodeId): NodeId | undefined {
    return this.genToOrig.get(gen);
  }
  generatedFrom(orig: NodeId): NodeId[] {
    return this.origToGen.get(orig) ?? [];
  }
}

export interface ExtractResult {
  program: Expr; // let <fn> = <params> -> <body'> in <tree with call site>
  fnName: string;
  fnBind: BindingId;
  fnNode: NodeId;
  params: Param[]; // first-occurrence order
  callSite: NodeId;
  sourceMap: SourceMap;
}

// A `return` crosses the extraction boundary iff its lexically enclosing
// lambda lies OUTSIDE the extracted subtree. Returns nested under a lambda
// that is fully inside the subtree are safe to move.
export function crossesReturnBoundary(e: Expr, insideLam = false): boolean {
  switch (e.k) {
    case 'return':
      return !insideLam || crossesReturnBoundary(e.value, true);
    case 'lam':
      return crossesReturnBoundary(e.body, true);
    case 'app':
      return crossesReturnBoundary(e.fn, insideLam) || crossesReturnBoundary(e.arg, insideLam);
    case 'let':
      return crossesReturnBoundary(e.value, insideLam) || crossesReturnBoundary(e.body, insideLam);
    case 'if':
      return (
        crossesReturnBoundary(e.c, insideLam) ||
        crossesReturnBoundary(e.t, insideLam) ||
        crossesReturnBoundary(e.e, insideLam)
      );
    case 'prim':
      return e.args.some((a) => crossesReturnBoundary(a, insideLam));
    default:
      return false;
  }
}

function replaceNode(root: Expr, id: NodeId, replacement: Expr): Expr {
  if (root.id === id) return replacement;
  switch (root.k) {
    case 'lam':
      return { ...root, body: replaceNode(root.body, id, replacement) };
    case 'app':
      return { ...root, fn: replaceNode(root.fn, id, replacement), arg: replaceNode(root.arg, id, replacement) };
    case 'let':
      return { ...root, value: replaceNode(root.value, id, replacement), body: replaceNode(root.body, id, replacement) };
    case 'if':
      return {
        ...root,
        c: replaceNode(root.c, id, replacement),
        t: replaceNode(root.t, id, replacement),
        e: replaceNode(root.e, id, replacement),
      };
    case 'prim':
      return { ...root, args: root.args.map((a) => replaceNode(a, id, replacement)) };
    case 'return':
      return { ...root, value: replaceNode(root.value, id, replacement) };
    default:
      return root;
  }
}

// Deep-copy the subtree with fresh node ids, recording the source map.
// Free-var refs in `remap` are rewritten to the new parameter bindings;
// every other ref (locals bound inside the subtree) keeps its identity.
function copySubtree(
  e: Expr,
  remap: Map<BindingId, { name: string; bind: BindingId }>,
  sm: SourceMap,
): Expr {
  const nid = freshNodeId();
  let out: Expr;
  switch (e.k) {
    case 'lit':
      out = { ...e, id: nid };
      break;
    case 'var': {
      const r = remap.get(e.ref);
      out = r ? { k: 'var', id: nid, name: r.name, ref: r.bind } : { ...e, id: nid };
      break;
    }
    case 'lam':
      out = { k: 'lam', id: nid, param: e.param, bind: e.bind, body: copySubtree(e.body, remap, sm) };
      break;
    case 'app':
      out = { k: 'app', id: nid, fn: copySubtree(e.fn, remap, sm), arg: copySubtree(e.arg, remap, sm) };
      break;
    case 'let':
      out = {
        k: 'let',
        id: nid,
        name: e.name,
        bind: e.bind,
        value: copySubtree(e.value, remap, sm),
        body: copySubtree(e.body, remap, sm),
      };
      break;
    case 'if':
      out = {
        k: 'if',
        id: nid,
        c: copySubtree(e.c, remap, sm),
        t: copySubtree(e.t, remap, sm),
        e: copySubtree(e.e, remap, sm),
      };
      break;
    case 'prim':
      out = { k: 'prim', id: nid, op: e.op, args: e.args.map((a) => copySubtree(a, remap, sm)) };
      break;
    case 'return':
      out = { k: 'return', id: nid, value: copySubtree(e.value, remap, sm) };
      break;
  }
  sm.add(nid, e.id);
  return out;
}

let fnCounter = 1;

export function extract(root: Expr, targetId: NodeId, fnName?: string): ExtractResult {
  const target = findNode(root, targetId);
  if (!target) throw new ExtractError(`node #${targetId} not found`);
  if (crossesReturnBoundary(target)) {
    throw new ExtractError(`extraction of node #${targetId} crosses a return control boundary`);
  }

  // Captured variables: free vars of the subtree, first-occurrence order.
  const fvs = freeVars(target);
  const usedNames = new Set<string>();
  const remap = new Map<BindingId, { name: string; bind: BindingId }>();
  const params: Param[] = [];
  for (const fv of fvs) {
    let name = fv.name;
    let i = 1;
    while (usedNames.has(name)) name = `${fv.name}$${i++}`;
    usedNames.add(name);
    const bind = freshBindId();
    remap.set(fv.ref, { name, bind });
    params.push({ name, bind, capturedFrom: fv.ref });
  }

  const sm = new SourceMap();
  const body = copySubtree(target, remap, sm);

  let fn: Expr = body;
  for (let i = params.length - 1; i >= 0; i--) {
    fn = { k: 'lam', id: freshNodeId(), param: params[i].name, bind: params[i].bind, body: fn };
  }

  const name = fnName ?? `extracted$${fnCounter++}`;
  const fnBind = freshBindId();

  // Call site: original refs are passed through untouched, so captured
  // closures keep their object identity in the environment.
  let call: Expr = { k: 'var', id: freshNodeId(), name, ref: fnBind };
  for (const fv of fvs) {
    call = {
      k: 'app',
      id: freshNodeId(),
      fn: call,
      arg: { k: 'var', id: freshNodeId(), name: fv.name, ref: fv.ref },
    };
  }

  sm.callSite = call.id;
  sm.target = target.id;
  sm.fnNode = fn.id;

  const program: Expr = {
    k: 'let',
    id: freshNodeId(),
    name,
    bind: fnBind,
    value: fn,
    body: replaceNode(root, targetId, call),
  };

  return { program, fnName: name, fnBind, fnNode: fn.id, params, callSite: call.id, sourceMap: sm };
}
