/**
 * 计算流水 DAG：节点为常量/输入/四则运算/显式舍入/显式单位换算。
 * 求值产生完整 Trace：每个节点保留精确值（Rat）、单位与所应用规则的可复算描述。
 * 节点可标记 traced:false 表示该流水未记录此中间证据（用于"无法定位"判定）。
 */

import { Rat, roundRat } from './rational.ts';
import type { RoundRule } from './rational.ts';
import { Unit, UnitMismatchError, convertValue, divUnit, mulUnit } from './units.ts';

export interface RoundSpec {
  rule: RoundRule;
  scale: number; // 保留的小数位数
}

export type NodeDef =
  | { kind: 'input'; id: string; source: string; unit: Unit; traced?: boolean }
  | { kind: 'const'; id: string; value: Rat; unit: Unit; traced?: boolean }
  | { kind: 'add'; id: string; args: [string, string]; traced?: boolean }
  | { kind: 'sub'; id: string; args: [string, string]; traced?: boolean }
  | { kind: 'mul'; id: string; args: [string, string]; traced?: boolean }
  | { kind: 'div'; id: string; args: [string, string]; traced?: boolean }
  | { kind: 'round'; id: string; args: [string]; spec: RoundSpec; traced?: boolean }
  | { kind: 'convert'; id: string; args: [string]; to: Unit; traced?: boolean };

export interface PipelineDef {
  name: string;
  nodes: NodeDef[];
  output: string;
}

export interface TraceEntry {
  id: string;
  kind: NodeDef['kind'];
  /** 精确值；evidence=false 时为 null（该流水未记录此中间值）。 */
  value: Rat | null;
  unit: Unit | null;
  evidence: boolean;
  /** 该节点所应用规则的可复算描述，如 "round half-up @2dp"。 */
  detail: string;
}

export class Trace {
  readonly entries = new Map<string, TraceEntry>();
  private readonly outputId: string;
  constructor(outputId: string) {
    this.outputId = outputId;
  }
  get(id: string): TraceEntry {
    const e = this.entries.get(id);
    if (!e) throw new Error(`Trace 中不存在节点 ${id}`);
    return e;
  }
  get output(): TraceEntry {
    return this.get(this.outputId);
  }
}

export class Pipeline {
  readonly def: PipelineDef;
  readonly order: string[] = [];
  private readonly map = new Map<string, NodeDef>();
  private readonly consumerMap = new Map<string, string[]>();

  constructor(def: PipelineDef) {
    this.def = def;
    for (const nd of def.nodes) {
      if (this.map.has(nd.id)) throw new Error(`重复节点 id: ${nd.id}`);
      const args = 'args' in nd ? nd.args : [];
      for (const a of args) {
        if (!this.map.has(a)) throw new Error(`节点 ${nd.id} 引用了未定义或后定义的节点 ${a}`);
      }
      this.map.set(nd.id, nd);
      this.order.push(nd.id);
      for (const a of args) {
        const list = this.consumerMap.get(a) ?? [];
        list.push(nd.id);
        this.consumerMap.set(a, list);
      }
    }
    if (!this.map.has(def.output)) throw new Error(`输出节点不存在: ${def.output}`);
  }

  get name(): string {
    return this.def.name;
  }
  get outputId(): string {
    return this.def.output;
  }
  node(id: string): NodeDef {
    const nd = this.map.get(id);
    if (!nd) throw new Error(`未知节点: ${id}`);
    return nd;
  }
  consumers(id: string): string[] {
    return this.consumerMap.get(id) ?? [];
  }
  /** 所有祖先（不含自身）。 */
  ancestors(id: string): Set<string> {
    const out = new Set<string>();
    const stack = [...('args' in this.node(id) ? this.nodeArgs(id) : [])];
    while (stack.length > 0) {
      const cur = stack.pop() as string;
      if (out.has(cur)) continue;
      out.add(cur);
      stack.push(...this.nodeArgs(cur));
    }
    return out;
  }
  nodeArgs(id: string): string[] {
    const nd = this.node(id);
    return 'args' in nd ? [...nd.args] : [];
  }

  /** 求值并生成完整 Trace。inputs 以 source 名为键，值为精确 Rat。 */
  evaluate(inputs: Readonly<Record<string, Rat>>): Trace {
    const trace = new Trace(this.def.output);
    const values = new Map<string, { value: Rat; unit: Unit }>();
    const record = (nd: NodeDef, value: Rat, unit: Unit, detail: string): void => {
      values.set(nd.id, { value, unit });
      const traced = nd.traced !== false;
      trace.entries.set(nd.id, {
        id: nd.id,
        kind: nd.kind,
        value: traced ? value : null,
        unit: traced ? unit : null,
        evidence: traced,
        detail,
      });
    };
    const need = (id: string): { value: Rat; unit: Unit } => {
      const v = values.get(id);
      if (!v) throw new Error(`内部错误：节点 ${id} 尚未求值`);
      return v;
    };

    for (const id of this.order) {
      const nd = this.node(id);
      switch (nd.kind) {
        case 'input': {
          const v = inputs[nd.source];
          if (!v) throw new Error(`缺少输入: ${nd.source}`);
          record(nd, v, nd.unit, `input ${nd.source} [${nd.unit.name}]`);
          break;
        }
        case 'const':
          record(nd, nd.value, nd.unit, `const ${nd.value.display()} [${nd.unit.name}]`);
          break;
        case 'add':
        case 'sub': {
          const x = need(nd.args[0]);
          const y = need(nd.args[1]);
          if (!x.unit.same(y.unit)) {
            throw new UnitMismatchError(
              `节点 ${nd.id}（${nd.kind}）两侧单位不一致：${x.unit.name} vs ${y.unit.name}；请先显式 convert`,
            );
          }
          const v = nd.kind === 'add' ? x.value.add(y.value) : x.value.sub(y.value);
          record(nd, v, x.unit, `${nd.kind} [${x.unit.name}]`);
          break;
        }
        case 'mul': {
          const x = need(nd.args[0]);
          const y = need(nd.args[1]);
          record(nd, x.value.mul(y.value), mulUnit(x.unit, y.unit), `mul`);
          break;
        }
        case 'div': {
          const x = need(nd.args[0]);
          const y = need(nd.args[1]);
          if (y.value.isZero()) throw new Error(`节点 ${nd.id} 除以零`);
          record(nd, x.value.div(y.value), divUnit(x.unit, y.unit), `div`);
          break;
        }
        case 'round': {
          const x = need(nd.args[0]);
          const v = roundRat(x.value, nd.spec.scale, nd.spec.rule);
          record(nd, v, x.unit, `round ${nd.spec.rule} @${nd.spec.scale}dp`);
          break;
        }
        case 'convert': {
          const x = need(nd.args[0]);
          const v = convertValue(x.value, x.unit, nd.to);
          record(nd, v, nd.to, `convert ${x.unit.name} -> ${nd.to.name} (×${x.unit.factor.div(nd.to.factor).display()})`);
          break;
        }
        default: {
          const neverNode: never = nd;
          throw new Error(`未知节点类型: ${JSON.stringify(neverNode)}`);
        }
      }
    }
    return trace;
  }
}
