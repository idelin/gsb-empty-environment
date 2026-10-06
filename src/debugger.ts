import type { Expr, Program } from './ast.ts';
import { parse, parseExpression } from './parser.ts';
import {
  display,
  Env,
  evalPureExpr,
  execProgram,
  isTruthy,
  RuntimeError,
  SideEffectError,
} from './interpreter.ts';
import type { BeforeEvent, Ctx, ExecGen, Value } from './interpreter.ts';

export interface Breakpoint {
  nodeId: number;
  condition?: string;
  conditionExpr?: Expr;
  hits: number;
}

export type ConditionErrorKind = 'side-effect' | 'evaluation';

export interface ConditionError {
  nodeId: number;
  condition: string;
  kind: ConditionErrorKind;
  message: string;
}

export interface TraceEntry {
  seq: number;
  nodeId: number;
  kind: string;
  text: string;
  depth: number;
}

export interface PauseState {
  nodeId: number;
  kind: string;
  text: string;
  line: number;
  depth: number;
  reason: 'step' | 'breakpoint';
}

export interface RunResult {
  output: string[];
  trace: TraceEntry[];
  error: RuntimeError | null;
  globals: Env;
}

export class Debugger {
  source: string;
  program: Program;
  output: string[] = [];
  trace: TraceEntry[] = [];
  conditionErrors: ConditionError[] = [];
  breakpoints: Map<number, Breakpoint> = new Map();
  error: RuntimeError | null = null;

  private globalEnv: Env;
  private ctx: Ctx;
  private gen: ExecGen;
  private pausedEvent: BeforeEvent | null = null;
  private finished = false;
  private suppressNodeId: number | null = null;

  constructor(source: string) {
    this.source = source;
    this.program = parse(source);
    this.globalEnv = new Env(null);
    this.ctx = { output: this.output, depth: 0 };
    this.globalEnv.define('print', {
      kind: 'native',
      name: 'print',
      fn: (args, ctx) => {
        ctx.output.push(args.map(display).join(' '));
        return null;
      },
    });
    this.gen = execProgram(this.program, this.globalEnv, this.ctx);
  }

  get isFinished(): boolean {
    return this.finished;
  }

  get globals(): Env {
    return this.globalEnv;
  }

  addBreakpoint(nodeId: number, condition?: string): void {
    const bp: Breakpoint = { nodeId, hits: 0 };
    if (condition !== undefined) {
      bp.condition = condition;
      bp.conditionExpr = parseExpression(condition);
    }
    this.breakpoints.set(nodeId, bp);
  }

  removeBreakpoint(nodeId: number): void {
    this.breakpoints.delete(nodeId);
  }

  step(): PauseState | null {
    return this.advance(false);
  }

  continue(): PauseState | null {
    this.suppressNodeId = this.pausedEvent ? this.pausedEvent.node.id : null;
    return this.advance(true);
  }

  runToEnd(): void {
    while (!this.finished) this.advance(true);
  }

  setVariable(name: string, value: Value): void {
    if (!this.pausedEvent) throw new Error('cannot set a variable while not paused');
    this.pausedEvent.env.setOrDefine(name, value);
  }

  getVariable(name: string): Value {
    const env = this.pausedEvent ? this.pausedEvent.env : this.globalEnv;
    const found = env.lookup(name);
    if (!found) throw new Error(`undefined variable '${name}'`);
    return found.vars.get(name) as Value;
  }

  private setPaused(event: BeforeEvent): void {
    this.pausedEvent = event;
  }

  private pauseState(reason: 'step' | 'breakpoint'): PauseState {
    const event = this.pausedEvent as BeforeEvent;
    return {
      nodeId: event.node.id,
      kind: event.node.kind,
      text: this.source.slice(event.node.start, event.node.end),
      line: event.node.line,
      depth: event.depth,
      reason,
    };
  }

  private recordTrace(event: BeforeEvent): void {
    this.trace.push({
      seq: this.trace.length,
      nodeId: event.node.id,
      kind: event.node.kind,
      text: this.source.slice(event.node.start, event.node.end),
      depth: event.depth,
    });
  }

  private advance(toBreakpoint: boolean): PauseState | null {
    if (this.finished) return null;
    while (true) {
      let result: IteratorResult<BeforeEvent, void>;
      try {
        result = this.gen.next();
      } catch (err) {
        this.finished = true;
        this.pausedEvent = null;
        if (err instanceof RuntimeError) {
          this.error = err;
          return null;
        }
        throw err;
      }
      if (result.done) {
        this.finished = true;
        this.pausedEvent = null;
        return null;
      }
      const event = result.value;
      this.recordTrace(event);
      if (!toBreakpoint) {
        this.setPaused(event);
        return this.pauseState('step');
      }
      if (this.suppressNodeId !== null) {
        if (event.node.id === this.suppressNodeId) continue;
        this.suppressNodeId = null;
      }
      const bp = this.breakpoints.get(event.node.id);
      if (!bp) continue;
      if (bp.conditionExpr) {
        let passed = false;
        try {
          passed = isTruthy(evalPureExpr(bp.conditionExpr, event.env));
        } catch (err) {
          const kind: ConditionErrorKind = err instanceof SideEffectError ? 'side-effect' : 'evaluation';
          this.conditionErrors.push({
            nodeId: event.node.id,
            condition: bp.condition as string,
            kind,
            message: (err as Error).message,
          });
          continue;
        }
        if (!passed) continue;
      }
      bp.hits++;
      this.setPaused(event);
      return this.pauseState('breakpoint');
    }
  }
}

export function runProgram(source: string): RunResult {
  const dbg = new Debugger(source);
  dbg.runToEnd();
  return { output: dbg.output, trace: dbg.trace, error: dbg.error, globals: dbg.globals };
}
