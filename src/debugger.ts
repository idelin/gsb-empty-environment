import { walkStatements, type Expr } from './ast.ts';
import { parse, parseExpression, ParseError } from './parser.ts';
import {
  collectEnvs,
  evalExpr,
  runProgramGen,
  truthy,
  type BeforeEvent,
  type Ctx,
  type Environment,
  type Value,
} from './interpreter.ts';

export interface ConditionErrorEntry {
  nodeId: number;
  line: number;
  condition: string;
  message: string;
  seq: number;
}

export interface TraceEntry {
  seq: number;
  nodeId: number;
  kind: string;
  line: number;
  reason: 'step' | 'breakpoint';
}

export interface PauseInfo {
  state: 'paused' | 'done';
  reason: 'breakpoint' | 'step' | 'end';
  nodeId?: number;
  kind?: string;
  line?: number;
}

interface Breakpoint {
  nodeId: number;
  condition?: string;
  condAst?: Expr;
  condParseError?: string;
}

interface SequencedEvent extends BeforeEvent {
  seq: number;
}

export class Debugger {
  readonly source: string;
  private readonly ctx: Ctx = { output: [] };
  private readonly gen: Generator<BeforeEvent, void, void>;
  private current: SequencedEvent | null = null;
  private seq = 0;
  private lastPauseSeq = -1;
  private readonly breakpoints = new Map<number, Breakpoint>();
  private readonly lineIndex = new Map<number, number>();
  readonly conditionErrors: ConditionErrorEntry[] = [];
  readonly trace: TraceEntry[] = [];
  private finished = false;

  constructor(source: string) {
    this.source = source;
    const program = parse(source);
    this.gen = runProgramGen(program, this.ctx);
    for (const stmt of walkStatements(program)) {
      if (!this.lineIndex.has(stmt.line)) this.lineIndex.set(stmt.line, stmt.id);
    }
  }

  get output(): string[] {
    return this.ctx.output;
  }

  get done(): boolean {
    return this.finished;
  }

  get currentLine(): number | null {
    return this.current ? this.current.node.line : null;
  }

  get currentNodeId(): number | null {
    return this.current ? this.current.node.id : null;
  }

  setBreakpoint(nodeId: number, condition?: string): void {
    this.breakpoints.set(nodeId, { nodeId, condition });
  }

  setBreakpointAtLine(line: number, condition?: string): number {
    const nodeId = this.lineIndex.get(line);
    if (nodeId === undefined) {
      throw new Error(`no statement starts on line ${line}`);
    }
    this.setBreakpoint(nodeId, condition);
    return nodeId;
  }

  clearBreakpoint(nodeId: number): void {
    this.breakpoints.delete(nodeId);
  }

  getVariable(name: string): Value | undefined {
    if (!this.current) return undefined;
    const env = this.current.env.lookupEnv(name);
    return env ? env.vars.get(name) : undefined;
  }

  setVariable(name: string, value: Value): void {
    if (!this.current) throw new Error('debugger is not paused');
    const env = this.current.env.lookupEnv(name);
    if (env) {
      env.vars.set(name, value);
    } else {
      this.current.env.declare(name, value);
    }
  }

  variables(): Record<string, Value> {
    if (!this.current) return {};
    const chain: Environment[] = [];
    let env: Environment | null = this.current.env;
    while (env) {
      chain.unshift(env);
      env = env.parent;
    }
    const merged: Record<string, Value> = {};
    for (const scope of chain) {
      for (const [key, value] of scope.vars) merged[key] = value;
    }
    return merged;
  }

  step(): PauseInfo {
    if (!this.advance()) return { state: 'done', reason: 'end' };
    return this.pause('step');
  }

  continue(): PauseInfo {
    while (true) {
      if (!this.advance()) return { state: 'done', reason: 'end' };
      const event = this.current!;
      // A single continue must never stop again on the exact node instance
      // it resumed from; each execution instance carries a unique seq.
      if (event.seq === this.lastPauseSeq) continue;
      const bp = this.breakpoints.get(event.node.id);
      if (!bp) continue;
      if (bp.condition !== undefined && !this.conditionPasses(bp, event)) continue;
      return this.pause('breakpoint');
    }
  }

  private advance(): boolean {
    if (this.finished) return false;
    const result = this.gen.next();
    if (result.done) {
      this.finished = true;
      this.current = null;
      return false;
    }
    this.seq++;
    this.current = { ...result.value, seq: this.seq };
    return true;
  }

  private pause(reason: 'step' | 'breakpoint'): PauseInfo {
    const event = this.current!;
    this.lastPauseSeq = event.seq;
    this.trace.push({
      seq: event.seq,
      nodeId: event.node.id,
      kind: event.node.kind,
      line: event.node.line,
      reason,
    });
    return {
      state: 'paused',
      reason,
      nodeId: event.node.id,
      kind: event.node.kind,
      line: event.node.line,
    };
  }

  private conditionPasses(bp: Breakpoint, event: SequencedEvent): boolean {
    try {
      if (bp.condParseError === undefined && bp.condAst === undefined) {
        try {
          bp.condAst = parseExpression(bp.condition!);
        } catch (err) {
          bp.condParseError = err instanceof ParseError ? err.message : String(err);
        }
      }
      if (bp.condParseError !== undefined) {
        throw new Error(`condition parse error: ${bp.condParseError}`);
      }
      return truthy(this.evalConditionPure(bp.condAst!, event.env));
    } catch (err) {
      this.conditionErrors.push({
        nodeId: event.node.id,
        line: event.node.line,
        condition: bp.condition!,
        message: err instanceof Error ? err.message : String(err),
        seq: event.seq,
      });
      return false;
    }
  }

  // Evaluates the condition against the live scopes but snapshots and
  // restores every reachable environment (and the output buffer), so the
  // evaluation is guaranteed to leave no observable side effects.
  private evalConditionPure(ast: Expr, env: Environment): Value {
    const saved = collectEnvs(env).map((scope) => ({ scope, vars: new Map(scope.vars) }));
    const savedOutput = this.ctx.output;
    this.ctx.output = [];
    try {
      const gen = evalExpr(ast, env, this.ctx);
      let result = gen.next();
      while (!result.done) result = gen.next();
      return result.value;
    } finally {
      for (const { scope, vars } of saved) {
        scope.vars.clear();
        for (const [key, value] of vars) scope.vars.set(key, value);
      }
      this.ctx.output = savedOutput;
    }
  }
}
