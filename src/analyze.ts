import type { Program } from "./model.ts";
import { validateProgram } from "./model.ts";
import {
  addFact,
  applyAssign,
  joinInto,
  killRootedAt,
  readTaint,
  tagKey,
  transferRead,
  type State,
  type TaintTag,
} from "./state.ts";

export interface AnalysisConfig {
  contextDepth?: number;
  maxAccessPathDepth?: number;
}

export interface PathStep {
  fn: string;
  via: string | null;
}

export interface TaintSourceRef {
  sourceId: string;
  fn: string;
  context: string[];
  path: PathStep[];
}

export interface Finding {
  sinkId: string;
  sinkFn: string;
  sinkContext: string[];
  sinkPath: PathStep[];
  sources: TaintSourceRef[];
}

export interface SummaryInfo {
  fn: string;
  context: string[];
  inputFacts: number;
  outputFacts: number;
}

export interface AnalysisResult {
  findings: Finding[];
  summaries: SummaryInfo[];
  iterations: number;
  instances: number;
}

interface Instance {
  key: string;
  fn: string;
  ctx: string[];
  input: State;
  output: State;
}

interface FindingAcc {
  sinkId: string;
  fn: string;
  ctx: string[];
  tags: Map<string, TaintTag>;
}

const keyOf = (fn: string, ctx: string[]): string => `${fn}@${JSON.stringify(ctx)}`;

export function analyze(program: Program, config: AnalysisConfig = {}): AnalysisResult {
  validateProgram(program);
  const contextDepth = config.contextDepth ?? 2;
  const maxAp = config.maxAccessPathDepth ?? 2;
  const fns = new Map(program.functions.map((f) => [f.name, f]));

  const instances = new Map<string, Instance>();
  const callersOf = new Map<string, Set<string>>();
  const edgeSet = new Set<string>();
  const edges: Array<{ from: string; site: string; to: string }> = [];
  const worklist: string[] = [];
  const queued = new Set<string>();
  const findingAcc = new Map<string, FindingAcc>();

  const enqueue = (key: string): void => {
    if (!queued.has(key)) {
      queued.add(key);
      worklist.push(key);
    }
  };

  const getInstance = (fn: string, ctx: string[]): Instance => {
    const key = keyOf(fn, ctx);
    let inst = instances.get(key);
    if (!inst) {
      inst = { key, fn, ctx, input: new Map(), output: new Map() };
      instances.set(key, inst);
      enqueue(key);
    }
    return inst;
  };

  const evaluate = (inst: Instance): void => {
    const decl = fns.get(inst.fn)!;
    const state: State = new Map();
    joinInto(state, inst.input, maxAp);
    const output: State = new Map();

    for (const stmt of decl.body) {
      switch (stmt.kind) {
        case "source": {
          const tag: TaintTag = { sourceId: stmt.id, fn: inst.fn, ctx: [...inst.ctx] };
          addFact(state, { base: stmt.target.base, fields: stmt.target.fields, tag }, maxAp);
          break;
        }
        case "assign":
          applyAssign(state, stmt.target, stmt.from, maxAp);
          break;
        case "sanitize":
          killRootedAt(state, stmt.target.base, stmt.target.fields);
          break;
        case "sink": {
          const tags = new Map<string, TaintTag>();
          for (const arg of stmt.args) {
            for (const tag of readTaint(state, arg.base, arg.fields)) {
              tags.set(tagKey(tag), tag);
            }
          }
          if (tags.size > 0) {
            const fkey = `${stmt.id}@${inst.key}`;
            let acc = findingAcc.get(fkey);
            if (!acc) {
              acc = { sinkId: stmt.id, fn: inst.fn, ctx: [...inst.ctx], tags: new Map() };
              findingAcc.set(fkey, acc);
            }
            for (const [k, t] of tags) acc.tags.set(k, t);
          }
          break;
        }
        case "call": {
          const calleeDecl = fns.get(stmt.callee)!;
          const fullCtx = [...inst.ctx, stmt.site];
          const calleeCtx = fullCtx.slice(Math.max(fullCtx.length - contextDepth, 0));
          const callee = getInstance(stmt.callee, calleeCtx);
          const ekey = `${inst.key}|${stmt.site}|${callee.key}`;
          if (!edgeSet.has(ekey)) {
            edgeSet.add(ekey);
            edges.push({ from: inst.key, site: stmt.site, to: callee.key });
            let set = callersOf.get(callee.key);
            if (!set) {
              set = new Set();
              callersOf.set(callee.key, set);
            }
            set.add(inst.key);
          }
          let changed = false;
          stmt.args.forEach((arg, i) => {
            const param = calleeDecl.params[i];
            if (param === undefined) return;
            for (const { suffix, tag } of transferRead(state, arg.base, arg.fields)) {
              if (addFact(callee.input, { base: param, fields: suffix, tag }, maxAp)) {
                changed = true;
              }
            }
          });
          if (changed) enqueue(callee.key);
          stmt.results.forEach((res, i) => {
            killRootedAt(state, res, []);
            for (const { suffix, tag } of transferRead(callee.output, `$ret${i}`, [])) {
              addFact(state, { base: res, fields: suffix, tag }, maxAp);
            }
          });
          break;
        }
        case "return": {
          stmt.values.forEach((value, i) => {
            for (const { suffix, tag } of transferRead(state, value.base, value.fields)) {
              addFact(output, { base: `$ret${i}`, fields: suffix, tag }, maxAp);
            }
          });
          break;
        }
      }
    }

    if (joinInto(inst.output, output, maxAp)) {
      const callers = callersOf.get(inst.key);
      if (callers) {
        for (const caller of callers) enqueue(caller);
      }
    }
  };

  const entryInst = getInstance(program.entry, []);
  enqueue(entryInst.key);

  let iterations = 0;
  while (worklist.length > 0) {
    const key = worklist.shift()!;
    queued.delete(key);
    const inst = instances.get(key)!;
    iterations += 1;
    evaluate(inst);
  }

  const adjacency = new Map<string, Array<{ site: string; to: string }>>();
  for (const e of edges) {
    let list = adjacency.get(e.from);
    if (!list) {
      list = [];
      adjacency.set(e.from, list);
    }
    list.push({ site: e.site, to: e.to });
  }

  const witness = (targetKey: string): PathStep[] => {
    const prev = new Map<string, { from: string; site: string }>();
    const queue = [entryInst.key];
    const seen = new Set([entryInst.key]);
    while (queue.length > 0) {
      const k = queue.shift()!;
      if (k === targetKey) break;
      for (const e of adjacency.get(k) ?? []) {
        if (!seen.has(e.to)) {
          seen.add(e.to);
          prev.set(e.to, { from: k, site: e.site });
          queue.push(e.to);
        }
      }
    }
    const steps: PathStep[] = [];
    let cur = targetKey;
    while (cur !== entryInst.key) {
      const p = prev.get(cur);
      if (!p) return [{ fn: program.entry, via: null }];
      steps.unshift({ fn: instances.get(cur)!.fn, via: p.site });
      cur = p.from;
    }
    steps.unshift({ fn: program.entry, via: null });
    return steps;
  };

  const findings: Finding[] = [];
  for (const acc of findingAcc.values()) {
    findings.push({
      sinkId: acc.sinkId,
      sinkFn: acc.fn,
      sinkContext: acc.ctx,
      sinkPath: witness(keyOf(acc.fn, acc.ctx)),
      sources: [...acc.tags.values()].map((t) => ({
        sourceId: t.sourceId,
        fn: t.fn,
        context: t.ctx,
        path: witness(keyOf(t.fn, t.ctx)),
      })),
    });
  }

  const summaries: SummaryInfo[] = [...instances.values()].map((i) => ({
    fn: i.fn,
    context: i.ctx,
    inputFacts: i.input.size,
    outputFacts: i.output.size,
  }));

  return { findings, summaries, iterations, instances: instances.size };
}
