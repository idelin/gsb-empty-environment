// Attribute definitions for the teaching language.
//
// Inherited (top-down) attributes:
//   scopeBase@block   environment at the entry of a block's scope
//   env@node          environment visible at a statement / expression
//
// Synthesized (bottom-up) attributes:
//   envOut@stmt       environment after a statement (decls add a binding)
//   cell@block:name   first decl of `name` in a block scope (or null)
//   bind@ref          name binding: decl node id the reference resolves to
//   type@node         type of an expression / declaration
//   value@node        constant value of an expression / const declaration
//   errors@node       diagnostics located at this node
//
// Scoping rules: a declaration is visible from the statements *after*
// it, inner scopes shadow outer ones, and a duplicate declaration in
// the same block is an error located at the later declaration.

import type {
  AnyNode,
  BinOp,
  Diagnostic,
  FuncDecl,
  NodeId,
  UnOp,
} from './ast.ts';
import { internEnv, lookupEnv, type Env } from './env.ts';

export interface View {
  node(id: NodeId): AnyNode | undefined;
  parent(id: NodeId): NodeId | null;
  stmtIndex(id: NodeId): number;
}

export interface TypeRes {
  t: string;
  err: string | null;
}

export type Value = number | boolean | 'unknown' | 'error';

const STMT_KINDS = new Set(['const', 'func', 'exprStmt', 'block']);

export function parseKey(key: string): { attr: string; nodeId: NodeId; name?: string } {
  if (key.startsWith('cell@')) {
    const i = key.indexOf(':');
    return { attr: 'cell', nodeId: key.slice('cell@'.length, i), name: key.slice(i + 1) };
  }
  const i = key.indexOf('@');
  return { attr: key.slice(0, i), nodeId: key.slice(i + 1) };
}

function mustNode(view: View, id: NodeId): AnyNode {
  const n = view.node(id);
  if (n === undefined) throw new Error(`unknown node ${id}`);
  return n;
}

type Get = (key: string) => unknown;

export function makeAttrFactory(view: View, get: Get): (key: string) => () => unknown {
  return (key) => {
    const { attr, nodeId, name } = parseKey(key);
    switch (attr) {
      case 'cell':
        return () => computeCell(view, nodeId, name as string);
      case 'scopeBase':
        return () => computeScopeBase(view, get, nodeId);
      case 'env':
        return () => computeEnv(view, get, nodeId);
      case 'envOut':
        return () => computeEnvOut(view, get, nodeId);
      case 'bind':
        return () => computeBind(view, get, nodeId);
      case 'type':
        return () => computeType(view, get, nodeId);
      case 'value':
        return () => computeValue(view, get, nodeId);
      case 'errors':
        return () => computeErrors(view, get, nodeId);
      default:
        throw new Error(`unknown attribute: ${key}`);
    }
  };
}

// --- scope cells ---------------------------------------------------------

// First binder of `name` in the block's own scope: function parameters
// (when the block is a function body) come before top-level statements.
function computeCell(view: View, blockId: NodeId, name: string): NodeId | null {
  const block = view.node(blockId);
  if (block === undefined || block.kind !== 'block') return null;
  const parentId = view.parent(blockId);
  const parent = parentId === null ? undefined : view.node(parentId);
  if (parent !== undefined && parent.kind === 'func') {
    for (const p of parent.params) if (p.name === name) return p.id;
  }
  for (const s of block.stmts) {
    if ((s.kind === 'const' || s.kind === 'func') && s.name === name) return s.id;
  }
  return null;
}

// --- inherited environment attributes ------------------------------------

function computeScopeBase(view: View, get: Get, blockId: NodeId): Env | null {
  const parentId = view.parent(blockId);
  if (parentId === null) return null;
  const parent = mustNode(view, parentId);
  if (parent.kind === 'program') return null;
  if (parent.kind === 'func') {
    // The function name is visible inside its own body (recursion);
    // parameters are then layered on top, first parameter wins on
    // duplicates (the later one is reported as a duplicate instead).
    let env = get(`env@${parentId}`) as Env | null;
    env = internEnv(env, parent.name, parent.id, blockId);
    const seen = new Set<string>();
    for (const p of parent.params) {
      if (seen.has(p.name)) continue;
      seen.add(p.name);
      env = internEnv(env, p.name, p.id, blockId);
    }
    return env;
  }
  // Nested block statement: inherits the environment at that statement.
  return get(`env@${blockId}`) as Env | null;
}

function computeEnv(view: View, get: Get, id: NodeId): Env | null {
  const node = mustNode(view, id);
  if (STMT_KINDS.has(node.kind)) {
    const parentId = view.parent(id);
    if (parentId === null) throw new Error(`statement ${id} has no parent block`);
    const idx = view.stmtIndex(id);
    if (idx === 0) return get(`scopeBase@${parentId}`) as Env | null;
    const parentBlock = mustNode(view, parentId);
    if (parentBlock.kind !== 'block') throw new Error(`parent of ${id} is not a block`);
    return get(`envOut@${parentBlock.stmts[idx - 1].id}`) as Env | null;
  }
  // Expression: environment of the nearest enclosing statement.
  let p = view.parent(id);
  while (p !== null) {
    const pn = view.node(p);
    if (pn !== undefined && STMT_KINDS.has(pn.kind)) return get(`env@${p}`) as Env | null;
    p = view.parent(p);
  }
  throw new Error(`no enclosing statement for ${id}`);
}

function computeEnvOut(view: View, get: Get, id: NodeId): Env | null {
  const node = mustNode(view, id);
  if (node.kind === 'const' || node.kind === 'func') {
    const blockId = view.parent(id) as NodeId;
    const first = get(`cell@${blockId}:${node.name}`) as NodeId | null;
    const env = get(`env@${id}`) as Env | null;
    // Only the first declaration of a name contributes a binding;
    // later duplicates are errors and leave the environment unchanged.
    return first === id ? internEnv(env, node.name, id, blockId) : env;
  }
  return get(`env@${id}`) as Env | null;
}

// --- name binding ----------------------------------------------------------

function computeBind(view: View, get: Get, id: NodeId): NodeId | null {
  const node = mustNode(view, id);
  if (node.kind !== 'ref') throw new Error(`bind is only defined for references, not ${node.kind}@${id}`);
  const env = get(`env@${id}`) as Env | null;
  // Record a dependency on the scope cell of every enclosing block,
  // including blocks that currently declare nothing at all: an unbound
  // reference is repaired when a declaration later appears in any of
  // its enclosing scopes.
  let p = view.parent(id);
  while (p !== null) {
    const pn = view.node(p);
    if (pn !== undefined && pn.kind === 'block') {
      get(`cell@${p}:${node.name}`);
    }
    p = view.parent(p);
  }
  const found = lookupEnv(env, node.name);
  return found === null ? null : found.declId;
}

// --- types -----------------------------------------------------------------

function ok(t: string): TypeRes {
  return { t, err: null };
}
function terr(err: string): TypeRes {
  return { t: 'error', err };
}

export function combineBinTypes(op: BinOp, lt: string, rt: string): TypeRes {
  if (lt === 'error' || rt === 'error') return ok('error');
  switch (op) {
    case '+':
    case '-':
    case '*':
      return lt === 'int' && rt === 'int'
        ? ok('int')
        : terr(`operator '${op}' expects int operands, got ${lt} and ${rt}`);
    case '<':
    case '<=':
    case '>':
    case '>=':
      return lt === 'int' && rt === 'int'
        ? ok('bool')
        : terr(`operator '${op}' expects int operands, got ${lt} and ${rt}`);
    case '==':
    case '!=':
      return lt === rt
        ? ok('bool')
        : terr(`operator '${op}' expects operands of the same type, got ${lt} and ${rt}`);
    case '&&':
    case '||':
      return lt === 'bool' && rt === 'bool'
        ? ok('bool')
        : terr(`operator '${op}' expects bool operands, got ${lt} and ${rt}`);
  }
}

export function combineUnType(op: UnOp, t: string): TypeRes {
  if (t === 'error') return ok('error');
  if (op === '-') {
    return t === 'int' ? ok('int') : terr(`operator '-' expects an int operand, got ${t}`);
  }
  return t === 'bool' ? ok('bool') : terr(`operator '!' expects a bool operand, got ${t}`);
}

function computeType(view: View, get: Get, id: NodeId): TypeRes {
  const node = mustNode(view, id);
  switch (node.kind) {
    case 'int':
      return ok('int');
    case 'bool':
      return ok('bool');
    case 'ref': {
      const decl = get(`bind@${id}`) as NodeId | null;
      if (decl === null) return terr(`unbound reference '${node.name}'`);
      return ok((get(`type@${decl}`) as TypeRes).t);
    }
    case 'bin': {
      const lt = (get(`type@${node.left.id}`) as TypeRes).t;
      const rt = (get(`type@${node.right.id}`) as TypeRes).t;
      return combineBinTypes(node.op, lt, rt);
    }
    case 'un':
      return combineUnType(node.op, (get(`type@${node.expr.id}`) as TypeRes).t);
    case 'const':
      return ok((get(`type@${node.init.id}`) as TypeRes).t);
    case 'func':
      return ok(`(${node.params.map((p) => p.paramType).join(',')})->${node.retType}`);
    case 'param':
      return ok(node.paramType);
    default:
      throw new Error(`type is not defined for ${node.kind}@${id}`);
  }
}

// --- constant values ---------------------------------------------------------

export function foldBin(op: BinOp, l: number | boolean, r: number | boolean): Value {
  switch (op) {
    case '+':
      return (l as number) + (r as number);
    case '-':
      return (l as number) - (r as number);
    case '*':
      return (l as number) * (r as number);
    case '<':
      return (l as number) < (r as number);
    case '<=':
      return (l as number) <= (r as number);
    case '>':
      return (l as number) > (r as number);
    case '>=':
      return (l as number) >= (r as number);
    case '==':
      return l === r;
    case '!=':
      return l !== r;
    case '&&':
      return (l as boolean) && (r as boolean);
    case '||':
      return (l as boolean) || (r as boolean);
  }
}

export function foldUn(op: UnOp, v: number | boolean): Value {
  return op === '-' ? -(v as number) : !(v as boolean);
}

function computeValue(view: View, get: Get, id: NodeId): Value {
  const node = mustNode(view, id);
  switch (node.kind) {
    case 'int':
      return node.value;
    case 'bool':
      return node.value;
    case 'ref': {
      const decl = get(`bind@${id}`) as NodeId | null;
      return decl === null ? 'error' : (get(`value@${decl}`) as Value);
    }
    case 'bin': {
      const t = get(`type@${id}`) as TypeRes;
      if (t.err !== null || t.t === 'error') return 'error';
      const l = get(`value@${node.left.id}`) as Value;
      const r = get(`value@${node.right.id}`) as Value;
      if (l === 'error' || r === 'error') return 'error';
      if (l === 'unknown' || r === 'unknown') return 'unknown';
      return foldBin(node.op, l, r);
    }
    case 'un': {
      const t = get(`type@${id}`) as TypeRes;
      if (t.err !== null || t.t === 'error') return 'error';
      const v = get(`value@${node.expr.id}`) as Value;
      if (v === 'error' || v === 'unknown') return v;
      return foldUn(node.op, v);
    }
    case 'const':
      return get(`value@${node.init.id}`) as Value;
    case 'func':
    case 'param':
      return 'unknown';
    default:
      throw new Error(`value is not defined for ${node.kind}@${id}`);
  }
}

// --- diagnostics -------------------------------------------------------------

function computeErrors(view: View, get: Get, id: NodeId): Diagnostic[] {
  const node = mustNode(view, id);
  switch (node.kind) {
    case 'const':
    case 'func': {
      const blockId = view.parent(id) as NodeId;
      const first = get(`cell@${blockId}:${node.name}`) as NodeId | null;
      return first === id
        ? []
        : [
            {
              nodeId: id,
              attr: 'envOut',
              message: `duplicate declaration of '${node.name}' in block ${blockId}`,
            },
          ];
    }
    case 'param': {
      const funcId = view.parent(id) as NodeId;
      const func = mustNode(view, funcId) as FuncDecl;
      const bodyId = func.body.id;
      const first = get(`cell@${bodyId}:${node.name}`) as NodeId | null;
      return first === id
        ? []
        : [
            {
              nodeId: id,
              attr: 'envOut',
              message: `duplicate declaration of '${node.name}' in block ${bodyId}`,
            },
          ];
    }
    case 'int':
    case 'bool':
    case 'ref':
    case 'bin':
    case 'un': {
      const t = get(`type@${id}`) as TypeRes;
      return t.err === null ? [] : [{ nodeId: id, attr: 'type', message: t.err }];
    }
    default:
      return [];
  }
}
