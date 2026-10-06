// Shared runtime pieces used by both the tail-call-optimizing interpreter
// (tco.ts) and the unoptimized reference interpreter (naive.ts).

export type Value = number | boolean | string;

export function showValue(v: Value): string {
  return typeof v === 'string' ? JSON.stringify(v) : String(v);
}

export class Env {
  private readonly vars = new Map<string, Value>();
  constructor(readonly parent: Env | null = null) {}
  define(name: string, value: Value): void {
    this.vars.set(name, value);
  }
  lookup(name: string): Value | undefined {
    for (let e: Env | null = this; e !== null; e = e.parent) {
      const v = e.vars.get(name);
      if (v !== undefined) return v;
    }
    return undefined;
  }
}

// ---------------------------------------------------------------------------
// Event log: records the observable call/return order of a run. Both
// interpreters emit the same event vocabulary so their logs can be diffed.
// ---------------------------------------------------------------------------

export type Event =
  | { tag: 'enter'; fn: string; args: Value[] }
  | { tag: 'exit'; fn: string; value: Value }
  | { tag: 'error'; message: string };

export class EventLog {
  readonly events: Event[] = [];
  enter(fn: string, args: Value[]): void {
    this.events.push({ tag: 'enter', fn, args: [...args] });
  }
  exit(fn: string, value: Value): void {
    this.events.push({ tag: 'exit', fn, value });
  }
  error(message: string): void {
    this.events.push({ tag: 'error', message });
  }
  serialize(): string[] {
    return this.events.map((e) => {
      switch (e.tag) {
        case 'enter':
          return `enter ${e.fn}(${e.args.map(showValue).join(', ')})`;
        case 'exit':
          return `exit ${e.fn} -> ${showValue(e.value)}`;
        case 'error':
          return `error ${e.message}`;
      }
    });
  }
}

// ---------------------------------------------------------------------------
// Error reporting. A CallTrace contains the FULL non-tail stack plus a
// bounded summary of the most recent tail calls.
// ---------------------------------------------------------------------------

export interface FrameInfo {
  fnName: string;
  args: Value[];
}

export interface TailRecord {
  ordinal: number; // 1-based global tail-call sequence number
  fnName: string;
  args: Value[];
}

export interface CallTrace {
  stack: FrameInfo[]; // outermost .. innermost non-tail frames
  tail: TailRecord[]; // oldest .. newest of the retained tail-call summaries
  totalTailCalls: number;
}

export interface TraceSource {
  snapshotStack(): FrameInfo[];
  snapshotTail(): TailRecord[];
  tailCallCount(): number;
  readonly log?: EventLog;
}

export class InterpError extends Error {
  constructor(message: string, readonly trace: CallTrace) {
    super(message);
    this.name = 'InterpError';
  }
}

export function raiseError(message: string, src: TraceSource): never {
  src.log?.error(message);
  throw new InterpError(message, {
    stack: src.snapshotStack(),
    tail: src.snapshotTail(),
    totalTailCalls: src.tailCallCount(),
  });
}

export function formatTrace(trace: CallTrace, message: string): string {
  const lines: string[] = ['Trace (most recent call last):'];
  for (const f of trace.stack) {
    lines.push(`  at ${f.fnName}(${f.args.map(showValue).join(', ')})`);
  }
  const omitted = trace.totalTailCalls - trace.tail.length;
  if (omitted > 0) lines.push(`  ... ${omitted} earlier tail call(s) omitted ...`);
  for (const t of trace.tail) {
    lines.push(`  tail #${t.ordinal} ${t.fnName}(${t.args.map(showValue).join(', ')})`);
  }
  lines.push(`Error: ${message}`);
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// Primitives (shared so both interpreters produce identical results/errors).
// ---------------------------------------------------------------------------

const PRIM_OPS = new Set([
  '+', '-', '*', 'div', 'mod',
  '<', '<=', '>', '>=', '=',
  'not', 'and', 'or',
]);

export function isPrimOp(name: string): boolean {
  return PRIM_OPS.has(name);
}

export function applyPrim(op: string, args: Value[], src: TraceSource): Value {
  const numbers = (): number[] => {
    for (const a of args) {
      if (typeof a !== 'number') {
        raiseError(`primitive '${op}' expects numbers, got ${showValue(a)}`, src);
      }
    }
    return args as number[];
  };
  const binary = (): [number, number] => {
    const ns = numbers();
    if (ns.length !== 2) raiseError(`primitive '${op}' expects 2 arguments, got ${ns.length}`, src);
    return [ns[0]!, ns[1]!];
  };
  switch (op) {
    case '+':
      return numbers().reduce((a, b) => a + b, 0);
    case '-': {
      const ns = numbers();
      if (ns.length === 0) raiseError(`primitive '-' expects at least 1 argument`, src);
      if (ns.length === 1) return -ns[0]!;
      return ns.slice(1).reduce((a, b) => a - b, ns[0]!);
    }
    case '*':
      return numbers().reduce((a, b) => a * b, 1);
    case 'div':
    case 'mod': {
      const [a, b] = binary();
      if (b === 0) raiseError(`division by zero in '${op}'`, src);
      return op === 'div' ? Math.trunc(a / b) : a % b;
    }
    case '<': { const [a, b] = binary(); return a < b; }
    case '<=': { const [a, b] = binary(); return a <= b; }
    case '>': { const [a, b] = binary(); return a > b; }
    case '>=': { const [a, b] = binary(); return a >= b; }
    case '=':
      if (args.length !== 2) raiseError(`primitive '=' expects 2 arguments, got ${args.length}`, src);
      return args[0] === args[1];
    case 'not':
      if (args.length !== 1 || typeof args[0] !== 'boolean') {
        raiseError(`primitive 'not' expects 1 boolean argument`, src);
      }
      return !args[0];
    default:
      raiseError(`unknown primitive '${op}'`, src);
  }
}
