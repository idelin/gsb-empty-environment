// Extract-function refactoring.
//
// Given a target subtree, produce a closed top-level function whose params
// are the subtree's free variables (in first-occurrence order), replace the
// subtree with a call, and emit a source map from original node ids to their
// destination. Extraction is rejected when the subtree contains a `return`
// whose nearest enclosing lambda lies outside the subtree (moving it would
// change which function the return escapes from).

import {
  childrenOf,
  findNode,
  forEachNode,
  maxNodeId,
  type Binder,
  type Expr,
  type FunDecl,
  type NodeId,
  type Program,
} from './ast.ts';
import { freeVarsOf } from './freevars.ts';

export type SourceTarget =
  | { where: 'function'; fn: string } // node moved into the extracted function
  | { where: 'root' }; // node stayed in the (rewritten) root expression

export interface CallSiteInfo {
  id: NodeId; // node id of the new call expression
  replaces: NodeId; // original subtree root it stands for
  fn: string;
  argRefs: number[]; // BindIds passed, in parameter order
}

export interface ExtractionOk {
  ok: true;
  program: Program;
  fn: FunDecl;
  params: Binder[]; // capture order
  callSite: CallSiteInfo;
  sourceMap: Map<NodeId, SourceTarget>;
}

export interface ExtractionErr {
  ok: false;
  reason: string;
}

export type Extraction = ExtractionOk | ExtractionErr;

function crossesReturnBoundary(subtree: Expr): boolean {
  let bad = false;
  const walk = (e: Expr, lamDepth: number): void => {
    if (bad) return;
    if (e.kind === 'return' && lamDepth === 0) {
      bad = true;
      return;
    }
    const next = e.kind === 'lam' ? lamDepth + 1 : lamDepth;
    for (const c of childrenOf(e)) walk(c, next);
  };
  walk(subtree, 0);
  return bad;
}

function replaceNode(root: Expr, target: NodeId, replacement: Expr): Expr {
  if (root.id === target) return replacement;
  switch (root.kind) {
    case 'num':
    case 'bool':
    case 'var':
      return root;
    case 'lam':
      return { ...root, body: replaceNode(root.body, target, replacement) };
    case 'app':
      return {
        ...root,
        fn: replaceNode(root.fn, target, replacement),
        args: root.args.map((a) => replaceNode(a, target, replacement)),
      };
    case 'let':
      return {
        ...root,
        value: replaceNode(root.value, target, replacement),
        body: replaceNode(root.body, target, replacement),
      };
    case 'if':
      return {
        ...root,
        cond: replaceNode(root.cond, target, replacement),
        then: replaceNode(root.then, target, replacement),
        els: replaceNode(root.els, target, replacement),
      };
    case 'bin':
      return {
        ...root,
        lhs: replaceNode(root.lhs, target, replacement),
        rhs: replaceNode(root.rhs, target, replacement),
      };
    case 'return':
      return { ...root, value: replaceNode(root.value, target, replacement) };
  }
}

export function extractFunction(
  root: Expr,
  targetId: NodeId,
  fnName = `extracted_${targetId}`,
): Extraction {
  const subtree = findNode(root, targetId);
  if (!subtree) return { ok: false, reason: `no node with id ${targetId}` };
  if (crossesReturnBoundary(subtree)) {
    return {
      ok: false,
      reason:
        'subtree contains a `return` whose enclosing function lies outside the extraction boundary',
    };
  }

  // Captured free variables become parameters, in first-occurrence order.
  // Distinct BindIds may share a surface name; dedupe parameter names so the
  // generated function stays well-formed (identity still lives in BindId).
  const seenNames = new Set<string>();
  const params: Binder[] = freeVarsOf(subtree).map((b) => {
    let name = b.name;
    if (seenNames.has(name)) name = `${name}_${b.bind}`;
    seenNames.add(name);
    return { bind: b.bind, name };
  });

  let fresh = maxNodeId(root) + 1;
  // Allocate a BindId for the new top-level function above every existing one.
  const maxBind = ((): number => {
    let m = -1;
    const scan = (e: Expr) =>
      forEachNode(e, (n) => {
        if (n.kind === 'lam') for (const p of n.params) m = Math.max(m, p.bind);
        if (n.kind === 'let') m = Math.max(m, n.binder.bind);
        if (n.kind === 'var') m = Math.max(m, n.ref);
      });
    scan(root);
    return m;
  })();
  const freshBind = maxBind + 1;

  const fn: FunDecl = {
    name: fnName,
    bind: freshBind,
    params,
    body: subtree, // moved as-is: BindId references stay valid
  };

  const callSiteId = fresh++;
  const call: Expr = {
    kind: 'app',
    id: callSiteId,
    fn: { kind: 'var', id: fresh++, name: fnName, ref: fn.bind },
    args: params.map((p) => ({
      kind: 'var',
      id: fresh++,
      name: p.name,
      ref: p.bind,
    })),
  };

  const newRoot = replaceNode(root, targetId, call);

  const sourceMap = new Map<NodeId, SourceTarget>();
  const moved = new Set<NodeId>();
  forEachNode(subtree, (n) => moved.add(n.id));
  forEachNode(root, (n) =>
    sourceMap.set(n.id, moved.has(n.id) ? { where: 'function', fn: fnName } : { where: 'root' }),
  );

  return {
    ok: true,
    program: { functions: [fn], root: newRoot },
    fn,
    params,
    callSite: {
      id: callSiteId,
      replaces: targetId,
      fn: fnName,
      argRefs: params.map((p) => p.bind),
    },
    sourceMap,
  };
}
