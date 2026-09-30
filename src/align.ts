/**
 * DAG 对齐：先按"来源规范化键"对齐（对加法/乘法做交换律+结合律展平，
 * 使 a+(b+c) 与 (a+b)+c、b*a 与 a*b 等价的中间步骤对齐），
 * 再对未匹配节点做位置回退匹配（同类节点且父节点互相对应），
 * 从而能发现"同一位置、不同规则"的分歧（如不同舍入规则、不同分母常量）。
 */

import type { NodeDef, Pipeline } from './dag.ts';

export interface AlignedPair {
  a: string;
  b: string;
  how: 'structural' | 'positional';
}

export interface Alignment {
  pairs: AlignedPair[];
  unmatchedA: string[];
  unmatchedB: string[];
}

function canonKey(p: Pipeline, id: string, memo: Map<string, string>): string {
  const hit = memo.get(id);
  if (hit) return hit;
  const nd = p.node(id);
  let key: string;
  switch (nd.kind) {
    case 'input':
      key = `in:${nd.source}`;
      break;
    case 'const':
      // 常量按值+维度对齐；换算因子不纳入键，使 1[m] 与 1[cm] 这类差异暴露为单位分歧
      key = `const:${nd.value.toString()}:${nd.unit.dimsKey()}`;
      break;
    case 'add':
    case 'mul': {
      const parts = flattenAC(p, nd, nd.kind, memo).sort();
      key = `${nd.kind}(${parts.join('|')})`;
      break;
    }
    case 'sub':
      key = `sub(${canonKey(p, nd.args[0], memo)}>${canonKey(p, nd.args[1], memo)})`;
      break;
    case 'div':
      key = `div(${canonKey(p, nd.args[0], memo)}>${canonKey(p, nd.args[1], memo)})`;
      break;
    case 'round':
      key = `round(${nd.spec.rule}@${nd.spec.scale}|${canonKey(p, nd.args[0], memo)})`;
      break;
    case 'convert':
      key = `convert(${nd.to.dimsKey()}|${canonKey(p, nd.args[0], memo)})`;
      break;
    default: {
      const neverNode: never = nd;
      throw new Error(`未知节点: ${JSON.stringify(neverNode)}`);
    }
  }
  memo.set(id, key);
  return key;
}

/** 交换律+结合律展平：收集同类（add 或 mul）嵌套链的所有操作数键。 */
function flattenAC(
  p: Pipeline,
  nd: NodeDef & { kind: 'add' | 'mul' },
  kind: 'add' | 'mul',
  memo: Map<string, string>,
): string[] {
  const out: string[] = [];
  for (const arg of nd.args) {
    const child = p.node(arg);
    if (child.kind === kind) {
      out.push(...flattenAC(p, child as NodeDef & { kind: 'add' | 'mul' }, kind, memo));
    } else {
      out.push(canonKey(p, arg, memo));
    }
  }
  return out;
}

/** 位置回退：A 节点的每个参数经当前匹配映射后，与 B 节点参数集合一致。 */
function argsCompatible(a: Pipeline, b: Pipeline, aId: string, bId: string, matchOf: Map<string, string>): boolean {
  const aArgs = a.nodeArgs(aId);
  const bArgs = new Set(b.nodeArgs(bId));
  if (aArgs.length !== bArgs.size) return false;
  for (const arg of aArgs) {
    const mapped = matchOf.get(arg);
    if (mapped === undefined || !bArgs.has(mapped)) return false;
  }
  return true;
}

export function align(a: Pipeline, b: Pipeline): Alignment {
  const memoA = new Map<string, string>();
  const memoB = new Map<string, string>();
  const keyA = new Map(a.order.map((id) => [id, canonKey(a, id, memoA)]));
  const keyB = new Map(b.order.map((id) => [id, canonKey(b, id, memoB)]));

  const pairs: AlignedPair[] = [];
  const matchedB = new Set<string>();
  const matchOf = new Map<string, string>(); // aId -> bId

  // 第一遍：结构键精确匹配（按拓扑序贪心一对一）
  for (const aId of a.order) {
    const k = keyA.get(aId) as string;
    const bId = b.order.find((id) => !matchedB.has(id) && keyB.get(id) === k);
    if (bId !== undefined) {
      pairs.push({ a: aId, b: bId, how: 'structural' });
      matchedB.add(bId);
      matchOf.set(aId, bId);
    }
  }

  // 第二遍：位置回退（同 kind、父节点互相对应；常量额外要求同维度）
  for (const aId of a.order) {
    if (matchOf.has(aId)) continue;
    const ndA = a.node(aId);
    if (ndA.kind === 'input') continue; // 来源名不同不应强行配对
    for (const bId of b.order) {
      if (matchedB.has(bId)) continue;
      const ndB = b.node(bId);
      if (ndB.kind !== ndA.kind) continue;
      if (ndA.kind === 'const' && ndB.kind === 'const') {
        if (!ndA.unit.sameDim(ndB.unit)) continue;
      } else if (!argsCompatible(a, b, aId, bId, matchOf)) {
        continue;
      }
      pairs.push({ a: aId, b: bId, how: 'positional' });
      matchedB.add(bId);
      matchOf.set(aId, bId);
      break;
    }
  }

  pairs.sort((x, y) => a.order.indexOf(x.a) - a.order.indexOf(y.a));
  return {
    pairs,
    unmatchedA: a.order.filter((id) => !matchOf.has(id)),
    unmatchedB: b.order.filter((id) => !matchedB.has(id)),
  };
}
