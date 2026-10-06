// Transformed (compiled) AST. All variable access is decomposed into explicit
// storage operations:
//
//   slot.*   - function-local stack slots, die with the frame
//   box.*    - heap boxes for captured MUTABLE locals; `box.new` runs where
//              the `let` ran, so each loop iteration / call instance gets a
//              fresh box, and sibling closures created in the same scope
//              execution observe the same box object
//   upval.*  - variables captured from enclosing functions; immutable
//              captures are copied by value, mutable ones arrive as boxes

import { BinOp } from '../ast';

export type TExpr =
  | { kind: 'num'; value: number }
  | { kind: 'bool'; value: boolean }
  | { kind: 'bin'; op: BinOp; left: TExpr; right: TExpr }
  | { kind: 'not'; expr: TExpr }
  | { kind: 'slot.get'; index: number }
  | { kind: 'box.get'; index: number }
  | { kind: 'upval.get'; index: number }
  | { kind: 'call'; fn: TExpr; args: TExpr[] }
  | { kind: 'closure'; fn: TFn }
  | { kind: 'arr'; items: TExpr[] }
  | { kind: 'index'; arr: TExpr; index: TExpr }
  | { kind: 'push'; arr: TExpr; value: TExpr }
  | { kind: 'len'; arr: TExpr };

export type TStmt =
  | { kind: 'slot.init'; index: number; value: TExpr }
  | { kind: 'slot.set'; index: number; value: TExpr }
  | { kind: 'box.new'; index: number; value: TExpr }
  | { kind: 'box.set'; index: number; value: TExpr }
  | { kind: 'upval.set'; index: number; value: TExpr }
  | { kind: 'expr'; expr: TExpr }
  | { kind: 'if'; cond: TExpr; then: TStmt[]; else: TStmt[] }
  | { kind: 'while'; cond: TExpr; body: TStmt[] }
  | { kind: 'return'; expr: TExpr | null }
  | { kind: 'print'; expr: TExpr };

// How a closure builds each of its upvalues from the enclosing frame and the
// enclosing closure at creation time.
export type Capture =
  | { from: 'parent-box'; index: number }   // boxed local of the parent frame
  | { from: 'parent-slot'; index: number }  // immutable local: copied by value
  | { from: 'parent-upval'; index: number }; // threaded through the parent

export interface ParamTarget {
  to: 'slot' | 'box';
  index: number;
}

export interface TFn {
  id: number;
  name: string;
  arity: number;
  params: ParamTarget[];
  selfSlot: number; // slot receiving the self closure, -1 if unnamed
  slotCount: number;
  boxCount: number;
  captures: Capture[];
  body: TStmt[];
}

export interface Compiled {
  top: TFn;
}
