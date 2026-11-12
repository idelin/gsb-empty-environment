import type { ObjectGraph } from './graph.ts';
import type { AccessorReport } from './xray.ts';
import { Xray } from './xray.ts';

export interface ExpandOptions {
  /** 最大展开深度，默认 8；到达深度的对象只输出引用标记。 */
  readonly maxDepth?: number;
  /** 是否包含类数组槽位，默认 true。 */
  readonly includeSlots?: boolean;
}

export interface CycleRef {
  readonly $cycle: string;
}

export interface DepthLimit {
  readonly $depthLimit: string;
}

export type ExpandedValue =
  | Primitive
  | ExpandedObject
  | ExpandedValue[]
  | AccessorReport
  | CycleRef
  | DepthLimit;

export interface ExpandedObject {
  readonly $node: string;
  readonly domain: string;
  readonly intrinsic: string;
  readonly nativeProto: string | null | undefined;
  readonly own: Record<string, ExpandedValue>;
  readonly slots?: ExpandedValue[];
}

type Primitive = string | number | bigint | boolean | null | undefined;

const isAccessorReport = (value: unknown): value is AccessorReport =>
  typeof value === 'object' && value !== null &&
  (value as AccessorReport).xrayAccessor === true;

/**
 * 把视图展开成可 JSON 化的纯数据快照。
 * 环回边输出 { $cycle }，深于 maxDepth 的节点输出 { $depthLimit }，
 * 因此任意互相引用的内容对象都不会导致递归死循环。
 */
export function expandXray(xray: Xray, rootId: string, options: ExpandOptions = {}): ExpandedValue {
  const maxDepth = options.maxDepth ?? 8;
  const includeSlots = options.includeSlots ?? true;
  const graph: ObjectGraph = xray.graph;
  const stack = new Set<string>();

  const expandValue = (value: unknown, depth: number): ExpandedValue => {
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) {
      return value as Primitive;
    }
    if (isAccessorReport(value)) return { ...value };
    if (Array.isArray(value)) return value.map((item) => expandValue(item, depth));
    const id = xray.nodeIdOf(value);
    if (!id) return String(value) as ExpandedValue;
    if (stack.has(id)) return { $cycle: id };
    if (depth >= maxDepth) return { $depthLimit: id };

    const node = graph.nodes[id];
    stack.add(id);
    const own: Record<string, ExpandedValue> = {};
    for (const key of Object.keys(node.nativeProps ?? {})) {
      own[key] = expandValue((value as Record<string, unknown>)[key], depth + 1);
    }
    const result: MutableExpandedObject = {
      $node: id,
      domain: node.domain,
      intrinsic: node.intrinsic,
      nativeProto: node.nativeProto,
      own,
    };
    if (includeSlots && node.arraySlots) {
      result.slots = node.arraySlots.map((slot) => expandValue(xray.wrapPublic(slot), depth + 1));
    }
    stack.delete(id);
    return result;
  };

  return expandValue(xray.view(rootId), 0);
}

interface MutableExpandedObject {
  $node: string;
  domain: string;
  intrinsic: string;
  nativeProto: string | null | undefined;
  own: Record<string, ExpandedValue>;
  slots?: ExpandedValue[];
}
