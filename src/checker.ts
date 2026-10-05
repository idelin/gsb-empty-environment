import {
  AggregateField,
  ExportPlan,
  FieldDef,
  Sensitivity,
  sensitivityLabel,
} from './types';

/** 来源链上的一步：某个字段经过某种变换。 */
export interface ChainStep {
  field: string;
  transform: FieldDef['kind'];
  note?: string;
}

/** 违规来源链：从敏感源字段到当前字段的完整传播路径。 */
export interface SourceChain {
  /** 链的起点（敏感源字段，或成功降级的聚合节点）。 */
  source: string;
  sourceSensitivity: Sensitivity;
  steps: ChainStep[];
}

export type ViolationType =
  | 'SENSITIVITY_EXCEEDED'
  | 'AGGREGATION_BELOW_MIN_GROUP'
  | 'FILTER_NO_REDUCTION'
  | 'UNKNOWN_FIELD'
  | 'CYCLIC_DEPENDENCY';

export interface Violation {
  type: ViolationType;
  field: string;
  message: string;
  chains: SourceChain[];
}

export interface ExportedFieldReport {
  field: string;
  effectiveSensitivity: Sensitivity;
  population: number;
  allowed: boolean;
  chains: SourceChain[];
}

export interface CheckResult {
  plan: string;
  ok: boolean;
  violations: Violation[];
  warnings: string[];
  exportedFields: ExportedFieldReport[];
  stats: {
    /** 实际求值的节点数（共享计算节点只计算一次）。 */
    evaluatedNodes: number;
    /** 被多个下游引用、且参与了本次求值的共享计算节点。 */
    sharedNodes: string[];
  };
}

interface EvaluatedNode {
  def: FieldDef;
  sensitivity: Sensitivity;
  population: number;
  chains: SourceChain[];
}

function inputsOf(def: FieldDef): string[] {
  switch (def.kind) {
    case 'source':
      return [];
    case 'derive':
      return def.inputs;
    case 'filter':
    case 'aggregate':
      return [def.input];
  }
}

function appendStep(chains: SourceChain[], step: ChainStep): SourceChain[] {
  return chains.map((chain) => ({
    ...chain,
    steps: [...chain.steps, step],
  }));
}

/** 保留贡献了当前最高敏感级别的来源链（并列时全部保留）。 */
function maxChains(inputs: EvaluatedNode[]): { level: Sensitivity; chains: SourceChain[] } {
  let level = Sensitivity.Public;
  for (const input of inputs) {
    if (input.sensitivity > level) level = input.sensitivity;
  }
  const chains: SourceChain[] = [];
  for (const input of inputs) {
    if (input.sensitivity === level) chains.push(...input.chains);
  }
  return { level, chains };
}

/** 聚合节点的有效最小组人数：实测值与均匀分布下界取较小者（保守）。 */
export function effectiveMinGroupSize(agg: AggregateField, inputPopulation: number): number {
  const uniformBound =
    agg.groupCount >= 1 ? Math.floor(inputPopulation / agg.groupCount) : 0;
  const observed = agg.observedMinGroupSize ?? Number.POSITIVE_INFINITY;
  return Math.min(observed, uniformBound);
}

/**
 * 导出计划检查入口。
 *
 * 从导出字段出发沿依赖图反向求值（记忆化，共享计算节点只求值一次）：
 * - 派生字段继承输入的最高敏感级别；
 * - 过滤字段减少人数、不改变敏感级别；
 * - 聚合字段仅在最小组人数达到 minGroupSize 时降级，否则级别继续传播；
 * - 导出字段的有效级别超过计划上限时产生违规，并输出完整来源链。
 */
export function checkExportPlan(fields: FieldDef[], plan: ExportPlan): CheckResult {
  const defs = new Map<string, FieldDef>();
  for (const def of fields) defs.set(def.name, def);

  const violations: Violation[] = [];
  const warnings: string[] = [];
  const memo = new Map<string, EvaluatedNode>();
  const inStack = new Set<string>();

  const evaluate = (name: string): EvaluatedNode | null => {
    const cached = memo.get(name);
    if (cached) return cached;

    const def = defs.get(name);
    if (!def) {
      violations.push({
        type: 'UNKNOWN_FIELD',
        field: name,
        message: `字段「${name}」不存在于字段定义中`,
        chains: [],
      });
      return null;
    }
    if (inStack.has(name)) {
      violations.push({
        type: 'CYCLIC_DEPENDENCY',
        field: name,
        message: `字段「${name}」存在循环依赖`,
        chains: [],
      });
      return null;
    }
    inStack.add(name);
    const node = evaluateDef(def);
    inStack.delete(name);
    if (node) memo.set(name, node);
    return node;
  };

  const evaluateDef = (def: FieldDef): EvaluatedNode | null => {
    switch (def.kind) {
      case 'source': {
        return {
          def,
          sensitivity: def.sensitivity,
          population: def.population,
          chains: [
            {
              source: def.name,
              sourceSensitivity: def.sensitivity,
              steps: [
                {
                  field: def.name,
                  transform: 'source',
                  note: `源字段，级别=${sensitivityLabel(def.sensitivity)}，人数=${def.population}`,
                },
              ],
            },
          ],
        };
      }

      case 'derive': {
        const inputs = def.inputs
          .map((input) => evaluate(input))
          .filter((node): node is EvaluatedNode => node !== null);
        if (inputs.length === 0) return null;
        const { level, chains } = maxChains(inputs);
        const population = Math.min(...inputs.map((input) => input.population));
        return {
          def,
          sensitivity: level,
          population,
          chains: appendStep(chains, {
            field: def.name,
            transform: 'derive',
            note: def.expression
              ? `派生计算：${def.expression}（继承最高级别 ${sensitivityLabel(level)}）`
              : `派生计算（继承最高级别 ${sensitivityLabel(level)}）`,
          }),
        };
      }

      case 'filter': {
        const input = evaluate(def.input);
        if (!input) return null;
        if (def.retainedPopulation > input.population) {
          violations.push({
            type: 'FILTER_NO_REDUCTION',
            field: def.name,
            message:
              `过滤字段「${def.name}」声明的保留人数 ${def.retainedPopulation} ` +
              `大于输入「${def.input}」的人数 ${input.population}，过滤后人数必须减少`,
            chains: input.chains,
          });
        } else if (def.retainedPopulation === input.population) {
          warnings.push(
            `过滤字段「${def.name}」未减少人数（${input.population}），请确认过滤条件是否生效`,
          );
        }
        return {
          def,
          sensitivity: input.sensitivity,
          population: def.retainedPopulation,
          chains: appendStep(input.chains, {
            field: def.name,
            transform: 'filter',
            note: `过滤${def.predicate ? `（${def.predicate}）` : ''}：人数 ${input.population} → ${def.retainedPopulation}`,
          }),
        };
      }

      case 'aggregate': {
        const input = evaluate(def.input);
        if (!input) return null;
        const minGroup = effectiveMinGroupSize(def, input.population);
        const downgradeOk = minGroup >= def.minGroupSize;
        if (!downgradeOk) {
          violations.push({
            type: 'AGGREGATION_BELOW_MIN_GROUP',
            field: def.name,
            message:
              `聚合字段「${def.name}」最小组人数 ${minGroup} 未达到最小分组人数 ` +
              `${def.minGroupSize}，禁止降级到${sensitivityLabel(def.downgradeTo)}，` +
              `敏感级别按 ${sensitivityLabel(input.sensitivity)} 继续传播`,
            chains: input.chains,
          });
        }
        if (downgradeOk) {
          const level = Math.min(input.sensitivity, def.downgradeTo) as Sensitivity;
          return {
            def,
            sensitivity: level,
            population: def.groupCount,
            chains: [
              {
                source: def.name,
                sourceSensitivity: level,
                steps: [
                  {
                    field: def.name,
                    transform: 'aggregate',
                    note:
                      `聚合降级生效：最小组人数 ${minGroup} ≥ 最小分组人数 ${def.minGroupSize}，` +
                      `级别降为${sensitivityLabel(level)}`,
                  },
                ],
              },
            ],
          };
        }
        return {
          def,
          sensitivity: input.sensitivity,
          population: def.groupCount,
          chains: appendStep(input.chains, {
            field: def.name,
            transform: 'aggregate',
            note:
              `聚合降级未生效：最小组人数 ${minGroup} < 最小分组人数 ${def.minGroupSize}，` +
              `级别仍为${sensitivityLabel(input.sensitivity)}`,
          }),
        };
      }
    }
  };

  const exportedFields: ExportedFieldReport[] = [];
  for (const name of plan.exportFields) {
    const node = evaluate(name);
    if (!node) continue;
    const allowed = node.sensitivity <= plan.maxAllowedSensitivity;
    if (!allowed) {
      violations.push({
        type: 'SENSITIVITY_EXCEEDED',
        field: name,
        message:
          `导出字段「${name}」有效敏感级别为${sensitivityLabel(node.sensitivity)}，` +
          `超过计划「${plan.name}」允许的最高级别${sensitivityLabel(plan.maxAllowedSensitivity)}`,
        chains: node.chains,
      });
    }
    exportedFields.push({
      field: name,
      effectiveSensitivity: node.sensitivity,
      population: node.population,
      allowed,
      chains: node.chains,
    });
  }

  // 共享计算节点：被多个字段（或导出计划）引用、且参与了本次求值。
  const refCount = new Map<string, number>();
  const bump = (field: string) => refCount.set(field, (refCount.get(field) ?? 0) + 1);
  for (const def of fields) for (const input of inputsOf(def)) bump(input);
  for (const name of plan.exportFields) bump(name);
  const sharedNodes = [...refCount.entries()]
    .filter(([name, count]) => count > 1 && memo.has(name))
    .map(([name]) => name)
    .sort();

  return {
    plan: plan.name,
    ok: violations.length === 0,
    violations,
    warnings,
    exportedFields,
    stats: { evaluatedNodes: memo.size, sharedNodes },
  };
}

/** 把来源链渲染为可读文本，便于审计报告输出。 */
export function formatChain(chain: SourceChain): string {
  const parts = chain.steps.map((step) => {
    const note = step.note ? `（${step.note}）` : '';
    return `${step.field}[${step.transform}]${note}`;
  });
  return parts.join(' → ');
}
