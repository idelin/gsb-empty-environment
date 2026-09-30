import { deepEqual, parsePath, type JSONValue } from './json.ts';
import type { Diagnostic } from './errors.ts';
import { typeExpr, type TypeCtx } from './expr.ts';
import { collectSteps, writesOf, type Cond, type Step } from './plan.ts';
import {
  mergeObjectSchemas,
  tLiteral,
  tNever,
  typeKey,
  unionOf,
  type Field,
  type Type,
} from './types.ts';

/** 单步推导记录：证书与增量缓存的基本单元。 */
export interface StepDerivation {
  stepId: string;
  op: string;
  reads: string[];
  writes: string[];
  inputHash: string;
  input: Type;
  output: Type;
  diagnostics: Diagnostic[];
}

export interface DepEdge {
  from: string;
  to: string;
  field: string;
}

export interface DepGraph {
  nodes: string[];
  edges: DepEdge[];
}

export interface Analysis {
  ok: boolean;
  diagnostics: Diagnostic[];
  schema: Type;
  derivations: StepDerivation[];
  depGraph: DepGraph;
}

export interface InferCtx {
  diagnostics: Diagnostic[];
  derivations: StepDerivation[];
  lastWriter: Map<string, string>;
  edges: DepEdge[];
}

export function newInferCtx(): InferCtx {
  return { diagnostics: [], derivations: [], lastWriter: new Map(), edges: [] };
}

function topField(path: string): string {
  const segs = parsePath(path);
  const first = segs[0];
  return typeof first === 'string' ? first : path;
}

function replaceField(schema: Type, name: string, f: Field): Type {
  if (schema.kind !== 'object') return schema;
  return { kind: 'object', fields: { ...schema.fields, [name]: f } };
}

function removeField(schema: Type, name: string): Type {
  if (schema.kind !== 'object') return schema;
  const fields = { ...schema.fields };
  delete fields[name];
  return { kind: 'object', fields };
}

/** 类型与字面量求交（用于 eq 条件真侧收缩）。 */
function intersectLiteral(t: Type, value: JSONValue): Type {
  if (t.kind === 'unknown') return tLiteral(value);
  if (t.kind === 'literal') return deepEqual(t.value, value) ? t : tNever;
  if (t.kind === 'union') return unionOf(t.options.map((o) => intersectLiteral(o, value)));
  const kindOf = typeof value;
  if (
    (value === null && t.kind === 'null') ||
    (kindOf === 'boolean' && t.kind === 'boolean') ||
    (kindOf === 'number' && t.kind === 'number') ||
    (kindOf === 'string' && t.kind === 'string')
  ) {
    return tLiteral(value);
  }
  return tNever;
}

/** 类型减去字面量（用于 eq 条件假侧收缩）；无法精确表示时保守保留。 */
function subtractLiteral(t: Type, value: JSONValue): Type {
  if (t.kind === 'literal') return deepEqual(t.value, value) ? tNever : t;
  if (t.kind === 'union') return unionOf(t.options.map((o) => subtractLiteral(o, value)));
  return t;
}

/**
 * 路径敏感条件收缩：返回收缩后的模式，null 表示该分支不可达。
 * and/or 在无法精确表示的极性下保守不收缩。
 */
export function narrow(schema: Type, cond: Cond, polarity: boolean): Type | null {
  switch (cond.kind) {
    case 'not':
      return narrow(schema, cond.cond, !polarity);
    case 'and': {
      if (!polarity) return schema; // !(a&&b) 无法精确表示，保守
      let cur: Type | null = schema;
      for (const c of cond.conds) {
        if (cur === null) return null;
        cur = narrow(cur, c, true);
      }
      return cur;
    }
    case 'or': {
      if (polarity) return schema; // a||b 无法精确表示，保守
      let cur: Type | null = schema;
      for (const c of cond.conds) {
        if (cur === null) return null;
        cur = narrow(cur, c, false);
      }
      return cur;
    }
    case 'has': {
      const segs = parsePath(cond.path);
      if (segs.length !== 1 || typeof segs[0] !== 'string' || schema.kind !== 'object') {
        return schema; // 仅对顶层字段做精确收缩，其余保守
      }
      const name = segs[0];
      const f = schema.fields[name];
      if (polarity) {
        if (!f) return null;
        return f.optional ? replaceField(schema, name, { ...f, optional: false }) : schema;
      }
      if (!f) return schema;
      if (!f.optional) return null;
      return removeField(schema, name);
    }
    case 'eq': {
      const segs = parsePath(cond.path);
      if (segs.length !== 1 || typeof segs[0] !== 'string' || schema.kind !== 'object') {
        return schema;
      }
      const name = segs[0];
      const f = schema.fields[name];
      if (!f) return polarity ? null : schema;
      if (polarity) {
        const inter = intersectLiteral(f.type, cond.value);
        if (inter.kind === 'never') return null;
        return replaceField(schema, name, { type: inter, optional: false });
      }
      const diff = subtractLiteral(f.type, cond.value);
      if (diff.kind === 'never') {
        return f.optional ? removeField(schema, name) : null;
      }
      return replaceField(schema, name, { ...f, type: diff });
    }
  }
}

function collectCondReads(cond: Cond, reads: Set<string>): void {
  switch (cond.kind) {
    case 'has':
    case 'eq':
      reads.add(cond.path);
      break;
    case 'not':
      collectCondReads(cond.cond, reads);
      break;
    case 'and':
    case 'or':
      for (const c of cond.conds) collectCondReads(c, reads);
      break;
  }
}

/** 记录依赖边（def-use）并更新最近写入者。 */
export function recordDeps(
  stepId: string,
  reads: string[],
  writes: string[],
  ctx: InferCtx,
  updateWriter: boolean,
): void {
  for (const r of reads) {
    const field = topField(r);
    const from = ctx.lastWriter.get(field) ?? '$input';
    ctx.edges.push({ from, to: stepId, field });
  }
  if (updateWriter) {
    for (const w of writes) ctx.lastWriter.set(topField(w), stepId);
  }
}

function inferBranch(
  step: Extract<Step, { op: 'branch' }>,
  schema: Type,
  ctx: InferCtx,
  own: Diagnostic[],
  reads: Set<string>,
  writes: Set<string>,
): Type {
  collectCondReads(step.cond, reads);
  const thenSchema = narrow(schema, step.cond, true);
  const elseSchema = narrow(schema, step.cond, false);
  if (thenSchema === null) {
    own.push({
      severity: 'warning',
      stepId: step.id,
      message: `then 分支不可达：条件在当前模式下恒为假`,
    });
  }
  if (elseSchema === null) {
    own.push({
      severity: 'warning',
      stepId: step.id,
      message: `else 分支不可达：条件在当前模式下恒为真`,
    });
  }
  // 分支自身诊断先于子步骤进入全局序列，保持来源顺序。
  ctx.diagnostics.push(...own);
  const thenOut = thenSchema === null ? tNever : inferStepList(step.then, thenSchema, ctx);
  const elseOut = elseSchema === null ? tNever : inferStepList(step.else, elseSchema, ctx);
  for (const w of writesOf(step)) writes.add(w);
  return mergeObjectSchemas(thenOut, elseOut);
}

/** 单步推导：返回输出模式，并把推导记录写入 ctx。 */
export function inferStep(step: Step, schema: Type, ctx: InferCtx): Type {
  const own: Diagnostic[] = [];
  const reads = new Set<string>();
  const writes = new Set<string>();
  const inputHash = typeKey(schema);
  const tctx: TypeCtx = { stepId: step.id, diagnostics: own, reads };
  let out: Type;

  if (schema.kind !== 'object' && step.op !== 'branch') {
    own.push({
      severity: 'error',
      stepId: step.id,
      message: `步骤作用于非对象模式（${schema.kind}）`,
    });
    out = schema;
  } else {
    switch (step.op) {
      case 'project': {
        const fields: Record<string, Field> = {};
        for (const f of step.fields) {
          reads.add('$.' + f);
          writes.add('$.' + f);
          const cur = (schema as Extract<Type, { kind: 'object' }>).fields[f];
          if (!cur) {
            own.push({
              severity: 'warning',
              stepId: step.id,
              path: '$.' + f,
              message: `投影包含不存在的字段 $.${f}`,
            });
          } else {
            fields[f] = cur;
          }
        }
        out = { kind: 'object', fields };
        break;
      }
      case 'rename': {
        reads.add('$.' + step.from);
        writes.add('$.' + step.from);
        writes.add('$.' + step.to);
        const obj = schema as Extract<Type, { kind: 'object' }>;
        const cur = obj.fields[step.from];
        if (!cur) {
          own.push({
            severity: 'error',
            stepId: step.id,
            path: '$.' + step.from,
            message: `改名来源字段不存在或已删除 $.${step.from}`,
          });
          out = schema;
        } else {
          if (obj.fields[step.to]) {
            own.push({
              severity: 'info',
              stepId: step.id,
              path: '$.' + step.to,
              message: `改名覆盖已有字段 $.${step.to}`,
            });
          }
          const fields = { ...obj.fields };
          delete fields[step.from];
          fields[step.to] = cur;
          out = { kind: 'object', fields };
        }
        break;
      }
      case 'compute': {
        const t = typeExpr(step.expr, schema, tctx);
        writes.add('$.' + step.target);
        out = replaceField(schema, step.target, { type: t, optional: false });
        break;
      }
      case 'readDynamic': {
        const t = typeExpr({ op: 'readDynamic', key: step.key }, schema, tctx);
        writes.add('$.' + step.target);
        out = replaceField(schema, step.target, { type: t, optional: false });
        break;
      }
      case 'delete': {
        writes.add('$.' + step.field);
        const obj = schema as Extract<Type, { kind: 'object' }>;
        if (!obj.fields[step.field]) {
          own.push({
            severity: 'info',
            stepId: step.id,
            path: '$.' + step.field,
            message: `删除不存在或已删除的字段 $.${step.field}（无操作）`,
          });
        }
        out = removeField(schema, step.field);
        break;
      }
      case 'branch': {
        out = inferBranch(step, schema, ctx, own, reads, writes);
        break;
      }
    }
  }

  if (step.op !== 'branch') ctx.diagnostics.push(...own);
  const readList = [...reads].sort();
  const writeList = [...writes].sort();
  // 分支的写入者已由子步骤登记，聚合写不再覆盖，保持依赖粒度。
  recordDeps(step.id, readList, writeList, ctx, step.op !== 'branch');
  ctx.derivations.push({
    stepId: step.id,
    op: step.op,
    reads: readList,
    writes: writeList,
    inputHash,
    input: schema,
    output: out,
    diagnostics: own,
  });
  return out;
}

export function inferStepList(steps: Step[], schema: Type, ctx: InferCtx): Type {
  let cur = schema;
  for (const s of steps) cur = inferStep(s, cur, ctx);
  return cur;
}

/** 编译入口：推导字段存在性与类型，产出诊断、依赖图与推导记录。 */
export function compilePlan(steps: Step[], inputSchema: Type): Analysis {
  const ctx = newInferCtx();
  const seen = new Set<string>();
  for (const s of collectSteps(steps)) {
    if (seen.has(s.id)) {
      ctx.diagnostics.push({
        severity: 'error',
        stepId: s.id,
        message: `步骤 id 重复: '${s.id}'`,
      });
    }
    seen.add(s.id);
  }
  const schema = inferStepList(steps, inputSchema, ctx);
  const nodes = ['$input', ...ctx.derivations.map((d) => d.stepId)];
  return {
    ok: !ctx.diagnostics.some((d) => d.severity === 'error'),
    diagnostics: ctx.diagnostics,
    schema,
    derivations: ctx.derivations,
    depGraph: { nodes, edges: ctx.edges },
  };
}
