// Machine for the transformed program.
//
// Storage model:
//   - every call pushes a fresh frame; unboxed locals/params live directly in
//     the frame ("stack storage") and vanish when the frame is popped
//   - boxed locals/params are heap-allocated { kind: 'box' } cells; a fresh box
//     is allocated each time the `let` statement executes (so loop iterations
//     and separate call instances get distinct boxes)
//   - closures capture explicitly: 'box' captures share the same box object
//     (siblings observe each other's writes), 'value' captures copy
//   - frames are never captured, so an unboxed local cannot escape

import type { Expr, TProgram, TStmt } from './ast.ts';
import { ReturnSignal, formatValue, truthy } from './common.ts';

export type MValue = number | boolean | null | MClosure;

export interface MBox {
  kind: 'box';
  value: MValue;
}

export interface MClosure {
  kind: 'closure';
  name: string;
  params: string[];
  boxedParams: string[];
  body: TStmt[];
  env: Map<string, MValue | MBox>;
}

type Slot = MValue | MBox;

export interface MachineStats {
  boxesAllocated: number;
}

export function runTransformed(
  prog: TProgram,
  out: string[],
): { result: MValue; stats: MachineStats } {
  const machine = new Machine(out);
  const result = machine.run(prog);
  return { result, stats: machine.stats };
}

function isBox(slot: Slot): slot is MBox {
  return typeof slot === 'object' && slot !== null && (slot as MBox).kind === 'box';
}

class Machine {
  frames: Map<string, Slot>[] = [new Map()];
  current: MClosure | null = null;
  stats: MachineStats = { boxesAllocated: 0 };
  out: string[];

  constructor(out: string[]) {
    this.out = out;
  }

  run(prog: TProgram): MValue {
    try {
      this.execBlock(prog);
    } catch (signal) {
      if (signal instanceof ReturnSignal) return signal.value as MValue;
      throw signal;
    }
    return null;
  }

  frame(): Map<string, Slot> {
    return this.frames[this.frames.length - 1];
  }

  newBox(value: MValue): MBox {
    this.stats.boxesAllocated += 1;
    return { kind: 'box', value };
  }

  findSlot(name: string): Map<string, Slot> | null {
    const top = this.frame();
    if (top.has(name)) return top;
    if (this.current !== null && this.current.env.has(name)) return this.current.env;
    const global = this.frames[0];
    if (global.has(name)) return global;
    return null;
  }

  getVar(name: string): MValue {
    const map = this.findSlot(name);
    if (map === null) throw new Error(`undefined variable: ${name}`);
    const raw = map.get(name) as Slot;
    return isBox(raw) ? raw.value : raw;
  }

  setVar(name: string, value: MValue): void {
    const map = this.findSlot(name);
    if (map === null) throw new Error(`undefined variable: ${name}`);
    const raw = map.get(name) as Slot;
    if (isBox(raw)) raw.value = value;
    else map.set(name, value);
  }

  execBlock(stmts: TStmt[]): void {
    for (const stmt of stmts) this.execStmt(stmt);
  }

  execStmt(stmt: TStmt): void {
    switch (stmt.tag) {
      case 'let': {
        const init = this.evalExpr(stmt.init);
        this.frame().set(stmt.name, stmt.boxed ? this.newBox(init) : init);
        return;
      }
      case 'assign':
        this.setVar(stmt.name, this.evalExpr(stmt.value));
        return;
      case 'func': {
        const env = new Map<string, MValue | MBox>();
        const closure: MClosure = {
          kind: 'closure',
          name: stmt.name,
          params: stmt.params,
          boxedParams: stmt.boxedParams,
          body: stmt.body,
          env,
        };
        for (const cap of stmt.captures) {
          const map = this.findSlot(cap.name);
          if (map === null) throw new Error(`undefined variable: ${cap.name}`);
          const raw = map.get(cap.name) as Slot;
          if (cap.by === 'box') {
            if (!isBox(raw)) throw new Error(`capture ${cap.name} is not boxed`);
            env.set(cap.name, raw); // share the same box object
          } else {
            env.set(cap.name, isBox(raw) ? raw.value : raw); // copy the value
          }
        }
        this.frame().set(stmt.name, closure);
        if (stmt.selfRef) env.set(stmt.name, closure);
        return;
      }
      case 'if':
        this.execBlock(truthy(this.evalExpr(stmt.cond)) ? stmt.then : stmt.else);
        return;
      case 'while':
        // Same frame every iteration: per-iteration freshness comes from
        // boxed `let` re-allocating its box each time the statement runs.
        while (truthy(this.evalExpr(stmt.cond))) this.execBlock(stmt.body);
        return;
      case 'return':
        throw new ReturnSignal(this.evalExpr(stmt.value));
      case 'print':
        this.out.push(formatValue(this.evalExpr(stmt.value)));
        return;
      case 'expr':
        this.evalExpr(stmt.value);
        return;
    }
  }

  evalExpr(expr: Expr): MValue {
    switch (expr.tag) {
      case 'num':
        return expr.value;
      case 'bool':
        return expr.value;
      case 'null':
        return null;
      case 'var':
        return this.getVar(expr.name);
      case 'bin': {
        const left = this.evalExpr(expr.left);
        const right = this.evalExpr(expr.right);
        switch (expr.op) {
          case '+':
            return (left as number) + (right as number);
          case '-':
            return (left as number) - (right as number);
          case '*':
            return (left as number) * (right as number);
          case '<':
            return (left as number) < (right as number);
          case '<=':
            return (left as number) <= (right as number);
          case '>':
            return (left as number) > (right as number);
          case '>=':
            return (left as number) >= (right as number);
          case '==':
            return left === right;
          case '!=':
            return left !== right;
        }
        throw new Error(`unknown operator: ${expr.op}`);
      }
      case 'call': {
        const fn = this.evalExpr(expr.fn);
        if (typeof fn !== 'object' || fn === null || fn.kind !== 'closure') {
          throw new Error('attempted to call a non-function');
        }
        return this.callClosure(fn, expr.args.map((arg) => this.evalExpr(arg)));
      }
    }
  }

  callClosure(closure: MClosure, args: MValue[]): MValue {
    const frame = new Map<string, Slot>();
    closure.params.forEach((param, index) => {
      const arg = index < args.length ? args[index] : null;
      frame.set(param, closure.boxedParams.includes(param) ? this.newBox(arg) : arg);
    });
    const previous = this.current;
    this.frames.push(frame);
    this.current = closure;
    try {
      this.execBlock(closure.body);
    } catch (signal) {
      if (signal instanceof ReturnSignal) return signal.value as MValue;
      throw signal;
    } finally {
      this.frames.pop();
      this.current = previous;
    }
    return null;
  }
}
