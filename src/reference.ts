import type { Program } from "./model.ts";
import { validateProgram } from "./model.ts";
import {
  addFact,
  applyAssign,
  joinInto,
  killRootedAt,
  readTaint,
  transferRead,
  type State,
  type TaintTag,
} from "./state.ts";
import type { AnalysisResult } from "./analyze.ts";

export interface RefFinding {
  sourceId: string;
  sinkId: string;
  sinkFn: string;
  path: string[];
}

export interface ReferenceConfig {
  maxDepth?: number;
  maxAccessPathDepth?: number;
}

export function referenceFindings(program: Program, config: ReferenceConfig = {}): RefFinding[] {
  validateProgram(program);
  const maxDepth = config.maxDepth ?? 6;
  const maxAp = config.maxAccessPathDepth ?? 2;
  const fns = new Map(program.functions.map((f) => [f.name, f]));
  const findings = new Map<string, RefFinding>();

  const exec = (fnName: string, input: State, path: string[], depthLeft: number): State => {
    const decl = fns.get(fnName)!;
    const state: State = new Map();
    joinInto(state, input, maxAp);
    const output: State = new Map();

    for (const stmt of decl.body) {
      switch (stmt.kind) {
        case "source": {
          const tag: TaintTag = { sourceId: stmt.id, fn: fnName, ctx: [...path] };
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
          for (const arg of stmt.args) {
            for (const tag of readTaint(state, arg.base, arg.fields)) {
              const key = `${tag.sourceId}|${stmt.id}|${fnName}|${path.join(">")}`;
              if (!findings.has(key)) {
                findings.set(key, {
                  sourceId: tag.sourceId,
                  sinkId: stmt.id,
                  sinkFn: fnName,
                  path: [...path],
                });
              }
            }
          }
          break;
        }
        case "call": {
          const calleeDecl = fns.get(stmt.callee)!;
          let calleeOut: State = new Map();
          if (depthLeft > 0) {
            const calleeIn: State = new Map();
            stmt.args.forEach((arg, i) => {
              const param = calleeDecl.params[i];
              if (param === undefined) return;
              for (const { suffix, tag } of transferRead(state, arg.base, arg.fields)) {
                addFact(calleeIn, { base: param, fields: suffix, tag }, maxAp);
              }
            });
            calleeOut = exec(stmt.callee, calleeIn, [...path, stmt.site], depthLeft - 1);
          }
          stmt.results.forEach((res, i) => {
            killRootedAt(state, res, []);
            for (const { suffix, tag } of transferRead(calleeOut, `$ret${i}`, [])) {
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
    return output;
  };

  exec(program.entry, new Map(), [], maxDepth);
  return [...findings.values()];
}

export interface CompletenessCheck {
  ok: boolean;
  missing: RefFinding[];
}

export function checkNoFalseNegatives(
  result: AnalysisResult,
  reference: RefFinding[],
): CompletenessCheck {
  const covered = new Set<string>();
  for (const finding of result.findings) {
    for (const src of finding.sources) {
      covered.add(`${src.sourceId}|${finding.sinkId}|${finding.sinkFn}`);
    }
  }
  const missing = reference.filter(
    (r) => !covered.has(`${r.sourceId}|${r.sinkId}|${r.sinkFn}`),
  );
  return { ok: missing.length === 0, missing };
}
