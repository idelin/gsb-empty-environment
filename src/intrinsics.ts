import type { GraphNode, ObjectGraph, XrayCallImpl } from './graph.ts';
import { ref } from './graph.ts';
import { Xray } from './xray.ts';

export const PRIVILEGED_DOMAIN = 'privileged';

export const ID = {
  ObjectPrototype: '%ObjectPrototype%',
  FunctionPrototype: '%FunctionPrototype%',
  ArrayPrototype: '%ArrayPrototype%',
  ObjectToString: '%Object.prototype.toString%',
  ObjectHasOwn: '%Object.prototype.hasOwnProperty%',
  ObjectValueOf: '%Object.prototype.valueOf%',
  FunctionToString: '%Function.prototype.toString%',
  ArrayJoin: '%Array.prototype.join%',
} as const;

const callable = (
  id: string,
  name: string,
  nativeProto: string,
  privilegedCall: XrayCallImpl,
): GraphNode => ({
  id,
  domain: PRIVILEGED_DOMAIN,
  intrinsic: 'Function',
  nativeProto: ref(nativeProto).$ref,
  nativeProps: {
    name: { kind: 'data', value: name, enumerable: false, configurable: true },
    length: { kind: 'data', value: 0, enumerable: false, configurable: true },
  },
  privilegedCall,
});

/**
 * 特权宿主维护的标准内置件。实现函数运行在特权侧，
 * 内容域即使替换了内容原型上的同名方法也无法影响它们。
 */
export function standardGraph(extraNodes: readonly GraphNode[] = []): ObjectGraph {
  const reserved = new Set<string>(Object.values(ID));
  for (const node of extraNodes) {
    if (reserved.has(node.id)) throw new TypeError(`reserved id used by caller: ${node.id}`);
  }
  const objectToString = callable(ID.ObjectToString, 'toString', ID.FunctionPrototype, (thisArg) => {
    const xray = Xray.xrayOf(thisArg);
    const id = xray?.nodeIdOf(thisArg as object);
    const intrinsic = id ? xray.graph.nodes[id].intrinsic : undefined;
    return `[object ${intrinsic ?? 'Object'}]`;
  });
  const objectHasOwn = callable(ID.ObjectHasOwn, 'hasOwnProperty', ID.FunctionPrototype, (thisArg, args) => {
    if (typeof args[0] !== 'string') return false;
    const xray = Xray.xrayOf(thisArg);
    const id = xray?.nodeIdOf(thisArg as object);
    if (!id) return false;
    const nativeProps = xray.graph.nodes[id].nativeProps;
    return nativeProps !== undefined &&
      Object.prototype.hasOwnProperty.call(nativeProps, args[0]);
  });
  const objectValueOf = callable(ID.ObjectValueOf, 'valueOf', ID.FunctionPrototype, (thisArg) => thisArg);
  const functionToString = callable(ID.FunctionToString, 'toString', ID.FunctionPrototype, () =>
    'function () { [native code] }');
  const functionCall = callable('%Function.prototype.call%', 'call', ID.FunctionPrototype, (thisArg, args, calleeId) => {
    const resolved = resolveCallee(thisArg, args, calleeId);
    const targetNode = resolved.xray.graph.nodes[resolved.targetId];
    if (!targetNode?.privilegedCall) throw new TypeError('callee is not an Xray function');
    return targetNode.privilegedCall(resolved.thisArg, resolved.restArgs, resolved.targetId);
  });
  const functionApply = callable('%Function.prototype.apply%', 'apply', ID.FunctionPrototype, (thisArg, args, calleeId) => {
    const resolved = resolveCallee(thisArg, args, calleeId);
    const targetNode = resolved.xray.graph.nodes[resolved.targetId];
    if (!targetNode?.privilegedCall) throw new TypeError('callee is not an Xray function');
    const callArgs = Array.isArray(args[1]) ? args[1] : [];
    return targetNode.privilegedCall(resolved.thisArg, callArgs, resolved.targetId);
  });
  const functionBind = callable('%Function.prototype.bind%', 'bind', ID.FunctionPrototype, (thisArg, args, calleeId) => {
    const resolved = resolveCallee(thisArg, args, calleeId);
    const targetNode = resolved.xray.graph.nodes[resolved.targetId];
    if (!targetNode?.privilegedCall) throw new TypeError('callee is not an Xray function');
    const boundThis = resolved.thisArg;
    const boundArgs = resolved.restArgs;
    const targetId = resolved.targetId;
    return (...rest: unknown[]) => targetNode.privilegedCall!(boundThis, [...boundArgs, ...rest], targetId);
  });
  const arrayJoin = callable(ID.ArrayJoin, 'join', ID.FunctionPrototype, (thisArg, args) => {
    const xray = Xray.xrayOf(thisArg);
    const id = xray?.nodeIdOf(thisArg as object);
    const slots = id ? xray.graph.nodes[id].arraySlots : undefined;
    if (!slots) throw new TypeError('Array.prototype.join called on non-array');
    const separator = typeof args[0] === 'string' ? args[0] : ',';
    const stringify = (slot: (typeof slots)[number]): string => {
      if (typeof slot === 'object' && slot !== null && '$ref' in slot) {
        const target = xray.graph.nodes[slot.$ref];
        return `[object ${target?.intrinsic ?? 'Object'}]`;
      }
      if (Array.isArray(slot)) return slot.map((item) => stringify(item)).join(separator);
      return slot === null || slot === undefined ? '' : String(slot);
    };
    return slots.map((slot) => stringify(slot)).join(separator);
  });

  const nodes: GraphNode[] = [
    {
      id: ID.FunctionPrototype,
      domain: PRIVILEGED_DOMAIN,
      intrinsic: 'FunctionPrototype',
      nativeProto: ID.ObjectPrototype,
      nativeProps: {
        constructor: { kind: 'data', value: ref(ID.FunctionPrototype), enumerable: false, configurable: true },
        toString: { kind: 'data', value: ref(ID.FunctionToString), enumerable: false, configurable: true },
        call: { kind: 'data', value: ref('%Function.prototype.call%'), enumerable: false, configurable: true },
        apply: { kind: 'data', value: ref('%Function.prototype.apply%'), enumerable: false, configurable: true },
        bind: { kind: 'data', value: ref('%Function.prototype.bind%'), enumerable: false, configurable: true },
      },
    },
    {
      id: ID.ObjectPrototype,
      domain: PRIVILEGED_DOMAIN,
      intrinsic: 'ObjectPrototype',
      nativeProto: null,
      nativeProps: {
        constructor: { kind: 'data', value: ref(ID.FunctionPrototype), enumerable: false, configurable: true },
        toString: { kind: 'data', value: ref(ID.ObjectToString), enumerable: false, configurable: true },
        hasOwnProperty: { kind: 'data', value: ref(ID.ObjectHasOwn), enumerable: false, configurable: true },
        valueOf: { kind: 'data', value: ref(ID.ObjectValueOf), enumerable: false, configurable: true },
      },
    },
    {
      id: ID.ArrayPrototype,
      domain: PRIVILEGED_DOMAIN,
      intrinsic: 'ArrayPrototype',
      nativeProto: ID.ObjectPrototype,
      nativeProps: {
        constructor: { kind: 'data', value: ref(ID.ArrayPrototype), enumerable: false, configurable: true },
        join: { kind: 'data', value: ref(ID.ArrayJoin), enumerable: false, configurable: true },
      },
    },
    objectToString,
    objectHasOwn,
    objectValueOf,
    functionToString,
    functionCall,
    functionApply,
    functionBind,
    arrayJoin,
    ...extraNodes,
  ];

  const record: Record<string, GraphNode> = {};
  for (const node of nodes) {
    if (record[node.id]) throw new TypeError(`duplicate object id: ${node.id}`);
    record[node.id] = node;
  }
  return { nodes: record };
}

export interface StandardRuntime {
  readonly xray: Xray;
  readonly graph: ObjectGraph;
}

export function createStandardRuntime(extraNodes: readonly GraphNode[] = []): StandardRuntime {
  const graph = standardGraph(extraNodes);
  return { graph, xray: new Xray(graph) };
}

/**
 * 解析 call/apply/bind 的被调者与 thisArg。
 * 方法形式调用时 thisArg 是被调函数视图（JS 原生 Function.prototype.call 语义）；
 * 裸调用退化时回退到 calleeId，首参仍作为 thisArg。
 */
function resolveCallee(
  receiverThis: unknown,
  args: readonly unknown[],
  calleeId: string,
): { xray: Xray; targetId: string; thisArg: unknown; restArgs: unknown[] } {
  const receiverXray = Xray.xrayOf(receiverThis);
  const receiverId = receiverXray?.nodeIdOf(receiverThis as object);
  if (receiverId && receiverId !== calleeId) {
    return {
      xray: receiverXray,
      targetId: receiverId,
      thisArg: args[0],
      restArgs: args.slice(1),
    };
  }
  const xray = Xray.forNode(calleeId);
  if (!xray) throw new TypeError('callee is not an Xray function');
  return { xray, targetId: calleeId, thisArg: args[0], restArgs: args.slice(1) };
}
