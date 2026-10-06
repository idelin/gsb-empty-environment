// The boxing transform: lowers the resolved source AST into the explicit
// storage AST (tast). Boxing policy:
//
//   captured + mutable  -> heap box (shared by sibling closures, fresh per
//                          `let` execution, hence per loop iteration and per
//                          call instance)
//   captured + const    -> stays a slot; children copy the value by-value
//   not captured        -> plain stack slot

import { Program, Decl, Stmt, Expr } from '../ast';
import { resolve, FnInfo } from './resolve';
import { Compiled, TFn, TStmt, TExpr, Capture, ParamTarget } from './tast';

interface Layout {
  area: 'slot' | 'box';
  index: number;
}

interface FrameShape {
  slotCount: number;
  boxCount: number;
}

export function compile(program: Program): Compiled {
  const { top, fnInfos } = resolve(program);
  const layouts = new Map<Decl, Layout>();
  const shapes = new Map<FnInfo, FrameShape>();
  let nextFnId = 0;

  const all: FnInfo[] = [top, ...fnInfos.values()];
  for (const info of all) {
    let slots = 0;
    let boxes = 0;
    for (const decl of info.decls) {
      if (decl.captured && decl.mutable) {
        layouts.set(decl, { area: 'box', index: boxes++ });
      } else {
        layouts.set(decl, { area: 'slot', index: slots++ });
      }
    }
    shapes.set(info, { slotCount: slots, boxCount: boxes });
  }

  return { top: buildFn(top) };

  function layoutOf(decl: Decl): Layout {
    const layout = layouts.get(decl);
    if (!layout) throw new Error(`internal: no layout for "${decl.name}"`);
    return layout;
  }

  function buildFn(info: FnInfo): TFn {
    const shape = shapes.get(info)!;
    const captures: Capture[] = [];
    if (info.parent) {
      for (const decl of info.freeVars) {
        if (decl.owner === info.parent.node) {
          const layout = layoutOf(decl);
          captures.push(
            layout.area === 'box'
              ? { from: 'parent-box', index: layout.index }
              : { from: 'parent-slot', index: layout.index },
          );
        } else {
          const index = info.parent.freeVars.indexOf(decl);
          if (index < 0) {
            throw new Error(`internal: parent does not thread "${decl.name}"`);
          }
          captures.push({ from: 'parent-upval', index });
        }
      }
    }

    const params: ParamTarget[] = [];
    let selfSlot = -1;
    if (info.node) {
      for (const decl of info.decls) {
        if (decl.isSelf) {
          selfSlot = layoutOf(decl).index; // self binding is const => slot
        } else if (decl.isParam) {
          const layout = layoutOf(decl);
          params.push({ to: layout.area, index: layout.index });
        }
      }
    }

    const body = tStmts(info, info.node ? info.node.body : program.body);
    const name = info.node ? info.node.selfName || '<anon>' : '<top>';
    return {
      id: nextFnId++,
      name,
      arity: params.length,
      params,
      selfSlot,
      slotCount: shape.slotCount,
      boxCount: shape.boxCount,
      captures,
      body,
    };
  }

  function localGet(info: FnInfo, decl: Decl): TExpr {
    const layout = layoutOf(decl);
    return layout.area === 'box'
      ? { kind: 'box.get', index: layout.index }
      : { kind: 'slot.get', index: layout.index };
  }

  function tStmts(info: FnInfo, stmts: Stmt[]): TStmt[] {
    const out: TStmt[] = [];
    for (const s of stmts) {
      if (s.kind === 'block') {
        out.push(...tStmts(info, s.body)); // blocks add no runtime structure
      } else {
        out.push(tStmt(info, s));
      }
    }
    return out;
  }

  function tStmt(info: FnInfo, s: Stmt): TStmt {
    switch (s.kind) {
      case 'let': {
        const layout = layoutOf(s.decl!);
        const init = tExpr(info, s.init);
        return layout.area === 'box'
          ? { kind: 'box.new', index: layout.index, value: init }
          : { kind: 'slot.init', index: layout.index, value: init };
      }
      case 'set': {
        const decl = s.decl!;
        const value = tExpr(info, s.value);
        if (decl.owner === info.node) {
          const layout = layoutOf(decl);
          return layout.area === 'box'
            ? { kind: 'box.set', index: layout.index, value }
            : { kind: 'slot.set', index: layout.index, value };
        }
        return { kind: 'upval.set', index: info.freeVars.indexOf(decl), value };
      }
      case 'expr':
        return { kind: 'expr', expr: tExpr(info, s.expr) };
      case 'if':
        return {
          kind: 'if',
          cond: tExpr(info, s.cond),
          then: tStmts(info, s.then),
          else: tStmts(info, s.else),
        };
      case 'while':
        return { kind: 'while', cond: tExpr(info, s.cond), body: tStmts(info, s.body) };
      case 'return':
        return { kind: 'return', expr: s.expr ? tExpr(info, s.expr) : null };
      case 'print':
        return { kind: 'print', expr: tExpr(info, s.expr) };
      case 'block':
        throw new Error('internal: blocks are flattened by tStmts');
    }
  }

  function tExpr(info: FnInfo, e: Expr): TExpr {
    switch (e.kind) {
      case 'num':
        return { kind: 'num', value: e.value };
      case 'bool':
        return { kind: 'bool', value: e.value };
      case 'var': {
        const decl = e.decl!;
        if (decl.owner === info.node) return localGet(info, decl);
        return { kind: 'upval.get', index: info.freeVars.indexOf(decl) };
      }
      case 'bin':
        return { kind: 'bin', op: e.op, left: tExpr(info, e.left), right: tExpr(info, e.right) };
      case 'not':
        return { kind: 'not', expr: tExpr(info, e.expr) };
      case 'call':
        return { kind: 'call', fn: tExpr(info, e.fn), args: e.args.map((a) => tExpr(info, a)) };
      case 'fn': {
        const child = fnInfos.get(e);
        if (!child) throw new Error('internal: unresolved fn expression');
        return { kind: 'closure', fn: buildFn(child) };
      }
      case 'arr':
        return { kind: 'arr', items: e.items.map((item) => tExpr(info, item)) };
      case 'index':
        return { kind: 'index', arr: tExpr(info, e.arr), index: tExpr(info, e.index) };
      case 'push':
        return { kind: 'push', arr: tExpr(info, e.arr), value: tExpr(info, e.value) };
      case 'len':
        return { kind: 'len', arr: tExpr(info, e.arr) };
    }
  }
}
