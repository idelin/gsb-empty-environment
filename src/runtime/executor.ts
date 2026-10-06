// Runtime for the transformed AST. Frames hold stack slots (die with the
// frame) and heap boxes (may be captured by closures). The executor keeps an
// explicit call stack and counters so tests can observe the storage model:
// boxes are allocated exactly where `box.new` executes, frames are popped on
// return, and closures only retain what they captured.

import { Compiled, TExpr, TFn, TStmt } from '../compiler/tast';
import { evalBinOp, truthy, formatValue } from '../semantics';

export type Value = number | boolean | null | TClosure | Value[];

export interface Box {
  value: Value;
}

export type Upval = { kind: 'box'; box: Box } | { kind: 'const'; value: Value };

export interface TClosure {
  fn: TFn;
  upvals: Upval[];
}

export function isClosure(v: Value): v is TClosure {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function upvalBox(closure: TClosure, index: number): Box | null {
  const upval = closure.upvals[index];
  return upval && upval.kind === 'box' ? upval.box : null;
}

class ReturnSignal {
  constructor(public value: Value) {}
}

class Frame {
  slots: Value[];
  boxes: Array<Box | null>;
  constructor(public fn: TFn, public closure: TClosure) {
    this.slots = new Array<Value>(fn.slotCount);
    this.boxes = new Array<Box | null>(fn.boxCount).fill(null);
  }
}

export interface ExecStats {
  boxesAllocated: number;
  framesPushed: number;
  framesPopped: number;
  maxDepth: number;
}

export interface ExecResult {
  value: Value | null;
  output: string[];
  printed: Value[]; // raw values, for box-identity assertions in tests
  stats: ExecStats;
}

function mustBox(box: Box | null): Box {
  if (!box) throw new Error('internal: box read before allocation');
  return box;
}

export function execute(compiled: Compiled): ExecResult {
  const stats: ExecStats = { boxesAllocated: 0, framesPushed: 0, framesPopped: 0, maxDepth: 0 };
  const output: string[] = [];
  const printed: Value[] = [];
  const stack: Frame[] = [];

  const topClosure: TClosure = { fn: compiled.top, upvals: [] };
  const value = call(topClosure, []);
  return { value, output, printed, stats };

  function call(closure: TClosure, args: Value[]): Value | null {
    const fn = closure.fn;
    if (args.length !== fn.arity) {
      throw new Error(`arity mismatch: expected ${fn.arity}, got ${args.length}`);
    }
    const frame = new Frame(fn, closure);
    stack.push(frame);
    stats.framesPushed++;
    if (stack.length > stats.maxDepth) stats.maxDepth = stack.length;
    try {
      for (let i = 0; i < fn.arity; i++) {
        const target = fn.params[i];
        if (target.to === 'box') {
          frame.boxes[target.index] = { value: args[i] };
          stats.boxesAllocated++;
        } else {
          frame.slots[target.index] = args[i];
        }
      }
      if (fn.selfSlot >= 0) frame.slots[fn.selfSlot] = closure;
      execStmts(frame, fn.body);
      return null;
    } catch (sig) {
      if (sig instanceof ReturnSignal) return sig.value;
      throw sig;
    } finally {
      stack.pop();
      stats.framesPopped++;
    }
  }

  function execStmts(frame: Frame, stmts: TStmt[]): void {
    for (const s of stmts) execStmt(frame, s);
  }

  function execStmt(frame: Frame, s: TStmt): void {
    switch (s.kind) {
      case 'slot.init':
      case 'slot.set':
        frame.slots[s.index] = evalExpr(frame, s.value);
        return;
      case 'box.new':
        frame.boxes[s.index] = { value: evalExpr(frame, s.value) };
        stats.boxesAllocated++;
        return;
      case 'box.set':
        mustBox(frame.boxes[s.index]).value = evalExpr(frame, s.value);
        return;
      case 'upval.set': {
        const upval = frame.closure.upvals[s.index];
        if (upval.kind !== 'box') {
          throw new Error('cannot assign to an immutable capture');
        }
        upval.box.value = evalExpr(frame, s.value);
        return;
      }
      case 'expr':
        evalExpr(frame, s.expr);
        return;
      case 'if':
        execStmts(frame, truthy(evalExpr(frame, s.cond)) ? s.then : s.else);
        return;
      case 'while':
        while (truthy(evalExpr(frame, s.cond))) execStmts(frame, s.body);
        return;
      case 'return':
        throw new ReturnSignal(s.expr ? evalExpr(frame, s.expr) : null);
      case 'print': {
        const v = evalExpr(frame, s.expr);
        output.push(formatValue(v));
        printed.push(v);
        return;
      }
    }
  }

  function evalExpr(frame: Frame, e: TExpr): Value {
    switch (e.kind) {
      case 'num':
      case 'bool':
        return e.value;
      case 'bin':
        return evalBinOp(e.op, evalExpr(frame, e.left), evalExpr(frame, e.right));
      case 'not':
        return !truthy(evalExpr(frame, e.expr));
      case 'slot.get':
        return frame.slots[e.index];
      case 'box.get':
        return mustBox(frame.boxes[e.index]).value;
      case 'upval.get': {
        const upval = frame.closure.upvals[e.index];
        return upval.kind === 'box' ? upval.box.value : upval.value;
      }
      case 'call': {
        const fnv = evalExpr(frame, e.fn);
        if (!isClosure(fnv)) throw new Error('cannot call non-function');
        const args = e.args.map((a) => evalExpr(frame, a));
        return call(fnv, args);
      }
      case 'closure': {
        const upvals: Upval[] = e.fn.captures.map((cap) => {
          switch (cap.from) {
            case 'parent-box':
              return { kind: 'box', box: mustBox(frame.boxes[cap.index]) };
            case 'parent-slot':
              return { kind: 'const', value: frame.slots[cap.index] };
            case 'parent-upval':
              return frame.closure.upvals[cap.index];
          }
        });
        return { fn: e.fn, upvals };
      }
      case 'arr':
        return e.items.map((item) => evalExpr(frame, item));
      case 'index': {
        const arr = evalExpr(frame, e.arr);
        const i = evalExpr(frame, e.index);
        if (!Array.isArray(arr) || typeof i !== 'number') {
          throw new Error('index expects (array, number)');
        }
        if (i < 0 || i >= arr.length) throw new Error('index out of bounds');
        return arr[i];
      }
      case 'push': {
        const arr = evalExpr(frame, e.arr);
        if (!Array.isArray(arr)) throw new Error('push expects an array');
        arr.push(evalExpr(frame, e.value));
        return arr;
      }
      case 'len': {
        const arr = evalExpr(frame, e.arr);
        if (!Array.isArray(arr)) throw new Error('len expects an array');
        return arr.length;
      }
    }
  }
}
