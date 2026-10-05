// Reference implementation: a plain from-scratch traversal with an
// explicit scope stack. No caching, no dependency tracking, no
// incrementality. The test-suite compares the incremental evaluator's
// snapshot against this after every edit.

import {
  walk,
  type Block,
  type Diagnostic,
  type Expr,
  type NodeId,
  type Program,
} from './ast.ts';
import type { Snapshot } from './program.ts';
import {
  combineBinTypes,
  combineUnType,
  foldBin,
  foldUn,
  type Value,
} from './semantics.ts';

type Frame = Array<[string, NodeId]>;

export function referenceCheck(root: Program): Snapshot {
  const envs: Record<NodeId, Array<[string, NodeId]>> = {};
  const binds: Record<NodeId, NodeId | null> = {};
  const types: Record<NodeId, string> = {};
  const values: Record<NodeId, Value> = {};
  const errors: Diagnostic[] = [];
  const declTypes = new Map<NodeId, string>();
  const declValues = new Map<NodeId, Value>();

  const frames: Frame[] = [];

  // Innermost frame first; within a frame, most recent binding first
  // (matches the order of the incremental evaluator's env cons list).
  const flatten = (): Array<[string, NodeId]> =>
    frames.slice().reverse().flatMap((f) => f.slice().reverse());

  const lookup = (name: string): NodeId | null => {
    for (let i = frames.length - 1; i >= 0; i -= 1) {
      const f = frames[i];
      for (let j = f.length - 1; j >= 0; j -= 1) {
        if (f[j][0] === name) return f[j][1];
      }
    }
    return null;
  };

  const checkExpr = (e: Expr): void => {
    switch (e.kind) {
      case 'int':
        types[e.id] = 'int';
        values[e.id] = e.value;
        return;
      case 'bool':
        types[e.id] = 'bool';
        values[e.id] = e.value;
        return;
      case 'ref': {
        const decl = lookup(e.name);
        binds[e.id] = decl;
        if (decl === null) {
          types[e.id] = 'error';
          values[e.id] = 'error';
          errors.push({ nodeId: e.id, attr: 'type', message: `unbound reference '${e.name}'` });
        } else {
          types[e.id] = declTypes.get(decl) as string;
          values[e.id] = declValues.get(decl) as Value;
        }
        return;
      }
      case 'bin': {
        checkExpr(e.left);
        checkExpr(e.right);
        const tres = combineBinTypes(e.op, types[e.left.id], types[e.right.id]);
        types[e.id] = tres.t;
        if (tres.err !== null) errors.push({ nodeId: e.id, attr: 'type', message: tres.err });
        const l = values[e.left.id];
        const r = values[e.right.id];
        values[e.id] =
          tres.err !== null || tres.t === 'error'
            ? 'error'
            : l === 'error' || r === 'error'
              ? 'error'
              : l === 'unknown' || r === 'unknown'
                ? 'unknown'
                : foldBin(e.op, l, r);
        return;
      }
      case 'un': {
        checkExpr(e.expr);
        const tres = combineUnType(e.op, types[e.expr.id]);
        types[e.id] = tres.t;
        if (tres.err !== null) errors.push({ nodeId: e.id, attr: 'type', message: tres.err });
        const v = values[e.expr.id];
        values[e.id] =
          tres.err !== null || tres.t === 'error'
            ? 'error'
            : v === 'error' || v === 'unknown'
              ? v
              : foldUn(e.op, v);
        return;
      }
    }
  };

  const checkBlock = (block: Block): void => {
    for (const s of block.stmts) {
      envs[s.id] = flatten();
      switch (s.kind) {
        case 'const': {
          checkExpr(s.init);
          types[s.id] = types[s.init.id];
          values[s.id] = values[s.init.id];
          declTypes.set(s.id, types[s.id]);
          declValues.set(s.id, values[s.id]);
          const frame = frames[frames.length - 1];
          if (frame.some(([n]) => n === s.name)) {
            errors.push({
              nodeId: s.id,
              attr: 'envOut',
              message: `duplicate declaration of '${s.name}' in block ${block.id}`,
            });
          } else {
            frame.push([s.name, s.id]);
          }
          break;
        }
        case 'func': {
          types[s.id] = `(${s.params.map((p) => p.paramType).join(',')})->${s.retType}`;
          declTypes.set(s.id, types[s.id]);
          declValues.set(s.id, 'unknown');
          // Body scope: function name frame, then the parameter frame
          // which is shared with the body's top-level declarations.
          frames.push([[s.name, s.id]]);
          const paramFrame: Frame = [];
          for (const p of s.params) {
            types[p.id] = p.paramType;
            values[p.id] = 'unknown';
            declTypes.set(p.id, p.paramType);
            declValues.set(p.id, 'unknown');
            if (paramFrame.some(([n]) => n === p.name)) {
              errors.push({
                nodeId: p.id,
                attr: 'envOut',
                message: `duplicate declaration of '${p.name}' in block ${s.body.id}`,
              });
            } else {
              paramFrame.push([p.name, p.id]);
            }
          }
          frames.push(paramFrame);
          checkBlock(s.body);
          frames.pop();
          frames.pop();
          const frame = frames[frames.length - 1];
          if (frame.some(([n]) => n === s.name)) {
            errors.push({
              nodeId: s.id,
              attr: 'envOut',
              message: `duplicate declaration of '${s.name}' in block ${block.id}`,
            });
          } else {
            frame.push([s.name, s.id]);
          }
          break;
        }
        case 'exprStmt':
          checkExpr(s.expr);
          break;
        case 'block':
          frames.push([]);
          checkBlock(s);
          frames.pop();
          break;
      }
    }
  };

  frames.push([]);
  checkBlock(root.body);
  frames.pop();

  const order = new Map<NodeId, number>();
  let i = 0;
  for (const n of walk(root)) {
    order.set(n.id, i);
    i += 1;
  }
  errors.sort(
    (a, b) =>
      (order.get(a.nodeId) as number) - (order.get(b.nodeId) as number) ||
      (a.attr < b.attr ? -1 : a.attr > b.attr ? 1 : 0),
  );

  return { envs, binds, types, values, errors };
}
