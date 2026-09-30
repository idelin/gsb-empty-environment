import { canonical, type JSONValue } from './json.ts';
import { exprReads } from './expr.ts';
import {
  compilePlan,
  inferStep,
  newInferCtx,
  recordDeps,
  type Analysis,
  type StepDerivation,
} from './infer.ts';
import type { Step } from './plan.ts';
import { typeKey, type Field, type Type } from './types.ts';

interface CacheEntry {
  key: string;
  derivations: StepDerivation[];
  output: Type;
}

/**
 * 增量推导引擎：以“步骤内容 + 其依赖字段切片”为缓存键。
 * 修改一个步骤只会失效自身及（传递）依赖其所写字段的推导；
 * 与改动无关的步骤直接命中缓存，不重新推导。
 *
 * 命中时不直接复用旧的整体输出模式，而是把缓存的“写入效应”
 * 重放到当前模式上（透传字段可能已被无关改动改变）。
 */
export class IncrementalEngine {
  private cache = new Map<string, CacheEntry>();
  /** 上一次 infer 中实际重新推导的步骤 id。 */
  lastRecomputed: string[] = [];
  /** 上一次 infer 中命中缓存的步骤 id。 */
  lastReused: string[] = [];

  private static relevantSlice(step: Step, schema: Type): string {
    if (schema.kind !== 'object') return typeKey(schema);
    const pick = (names: string[]): string => {
      const parts: string[] = [];
      for (const n of [...new Set(names)].sort()) {
        const f = schema.fields[n];
        parts.push(n + '=' + (f ? typeKey(f.type) + (f.optional ? '?' : '') : '∅'));
      }
      return parts.join(',');
    };
    switch (step.op) {
      case 'project':
        // 依赖全部字段名集合（不依赖字段类型）。
        return 'names:' + Object.keys(schema.fields).sort().join(',');
      case 'rename':
        return pick([step.from, step.to]);
      case 'compute':
        return pick(
          [...exprReads(step.expr)]
            .map((p) => p.replace(/^\$\./, '').split('.')[0]!)
            .concat([step.target]),
        );
      case 'delete':
        return pick([step.field]);
      case 'readDynamic':
      case 'branch':
        // 动态读取与分支保守依赖完整模式。
        return 'all:' + typeKey(schema);
    }
  }

  private static stepKey(step: Step, schema: Type): string {
    return (
      canonical(step as unknown as JSONValue) + '|' + IncrementalEngine.relevantSlice(step, schema)
    );
  }

  /** 字段局部步骤（project/rename/compute/delete）：在当前模式上重放缓存的写入效应。 */
  private static reapplyLocal(step: Step, schema: Type, cachedOutput: Type): Type {
    if (schema.kind !== 'object' || cachedOutput.kind !== 'object') return cachedOutput;
    switch (step.op) {
      case 'compute': {
        const written = cachedOutput.fields[step.target] as Field;
        return { kind: 'object', fields: { ...schema.fields, [step.target]: written } };
      }
      case 'delete': {
        const fields = { ...schema.fields };
        delete fields[step.field];
        return { kind: 'object', fields };
      }
      case 'rename': {
        const cur = schema.fields[step.from];
        if (!cur) return schema; // 与缓存时一致地报错（诊断已缓存）
        const fields = { ...schema.fields };
        delete fields[step.from];
        fields[step.to] = cur;
        return { kind: 'object', fields };
      }
      case 'project': {
        const fields: Record<string, Field> = {};
        for (const f of step.fields) {
          const cur = schema.fields[f];
          if (cur) fields[f] = cur;
        }
        return { kind: 'object', fields };
      }
      default:
        return cachedOutput;
    }
  }

  /** 增量推导整个计划；只重新计算被失效的步骤。 */
  infer(steps: Step[], inputSchema: Type): Analysis {
    const ctx = newInferCtx();
    this.lastRecomputed = [];
    this.lastReused = [];
    let schema = inputSchema;

    for (const step of steps) {
      const key = IncrementalEngine.stepKey(step, schema);
      const hit = this.cache.get(step.id);
      if (hit && hit.key === key) {
        if (
          step.op === 'compute' ||
          step.op === 'rename' ||
          step.op === 'delete' ||
          step.op === 'project'
        ) {
          // 字段局部步骤：在当前模式上重放写入效应，并刷新推导记录。
          const cached = hit.derivations[0]!;
          const output = IncrementalEngine.reapplyLocal(step, schema, cached.output);
          const refreshed: StepDerivation = {
            ...cached,
            input: schema,
            inputHash: typeKey(schema),
            output,
          };
          ctx.diagnostics.push(...refreshed.diagnostics);
          ctx.derivations.push(refreshed);
          recordDeps(refreshed.stepId, refreshed.reads, refreshed.writes, ctx, true);
          schema = output;
        } else {
          // 分支/动态读取：缓存键含完整模式，推导记录可原样重放。
          for (const d of hit.derivations) {
            ctx.diagnostics.push(...d.diagnostics);
            ctx.derivations.push(d);
            recordDeps(d.stepId, d.reads, d.writes, ctx, d.op !== 'branch');
          }
          schema = hit.output;
        }
        this.lastReused.push(step.id);
      } else {
        const derivStart = ctx.derivations.length;
        schema = inferStep(step, schema, ctx);
        this.cache.set(step.id, {
          key,
          derivations: ctx.derivations.slice(derivStart),
          output: schema,
        });
        this.lastRecomputed.push(step.id);
      }
    }
    const nodes = ['$input', ...ctx.derivations.map((d) => d.stepId)];
    return {
      ok: !ctx.diagnostics.some((d) => d.severity === 'error'),
      diagnostics: ctx.diagnostics,
      schema,
      derivations: ctx.derivations,
      depGraph: { nodes, edges: ctx.edges },
    };
  }

  /** 与全量编译结果对比，验证增量一致性（供测试与演示）。 */
  static matchesFull(steps: Step[], inputSchema: Type, inc: Analysis): boolean {
    const full = compilePlan(steps, inputSchema);
    return (
      canonical(full.schema as unknown as JSONValue) ===
        canonical(inc.schema as unknown as JSONValue) &&
      canonical(full.diagnostics as unknown as JSONValue) ===
        canonical(inc.diagnostics as unknown as JSONValue) &&
      canonical(full.depGraph as unknown as JSONValue) ===
        canonical(inc.depGraph as unknown as JSONValue)
    );
  }
}
