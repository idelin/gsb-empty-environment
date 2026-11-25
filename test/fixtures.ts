import { compileGraph, type CompiledGraph } from '../src/schema.ts';
import { encodeRootMessage } from '../src/encode-binary.ts';
import type { TypeGraph } from '../src/types.ts';

/**
 * 测试类型图（含递归、数组、别名、嵌套、旧版本迁移）。
 *
 * 类型编号：
 *   1 根对象 Root
 *   2 嵌套对象 Profile
 *   3 标签数组（string 元素）
 *   4 id64 标量
 *   5 int32 标量
 *   6 string 标量
 *   7 bool 标量
 *   8 角色数组（Profile 元素，递归）
 */
export function buildCurrentGraph(): CompiledGraph {
  const graph: TypeGraph = {
    version: 2,
    rootTypeNo: 1,
    types: {
      1: {
        kind: 'object',
        no: 1,
        name: 'Root',
        fields: [
          { no: 1, names: ['subjectId', 'sid'], typeNo: 4, required: true },
          { no: 2, names: ['profile'], typeNo: 2 },
          { no: 3, names: ['tags'], typeNo: 3 },
          { no: 4, names: ['enabled', 'active'], typeNo: 7 },
          { no: 5, names: ['sameNumberEverywhere'], typeNo: 5 },
        ],
      },
      2: {
        kind: 'object',
        no: 2,
        name: 'Profile',
        fields: [
          { no: 10, names: ['displayName', 'dn'], typeNo: 6 },
          { no: 11, names: ['score'], typeNo: 5 },
          { no: 12, names: ['delegates'], typeNo: 8 },
          { no: 13, names: ['legacyOwnerId'], typeNo: 4 },
        ],
      },
      3: { kind: 'array', no: 3, elementTypeNo: 6 },
      4: { kind: 'id64', no: 4 },
      5: { kind: 'int32', no: 5 },
      6: { kind: 'string', no: 6 },
      7: { kind: 'bool', no: 7 },
      8: { kind: 'array', no: 8, elementTypeNo: 2 },
    },
    migrations: [
      {
        fromVersion: 1,
        moves: [
          { fromFieldNo: 90, fromName: 'ownerId', toPath: [2, 13] },
          { fromFieldNo: 91, fromName: 'nick', toPath: [2, 10] },
        ],
      },
    ],
  };
  return compileGraph(graph);
}

/** 构造与当前图等价但 version=3 的换版图（结构完全一致，digest 随版本变化）。 */
export function buildRolloverGraph(): CompiledGraph {
  const compiled = buildCurrentGraph();
  const rolled: TypeGraph = {
    ...compiled.raw,
    version: 3,
    types: structuredClone(compiled.raw.types),
    migrations: structuredClone(compiled.raw.migrations),
  };
  return compileGraph(rolled);
}

export const tenant = 'tenant-7';

export interface EquivalentPayload {
  json: string;
  binary: Uint8Array;
}

/** 同一授权对象的两种入口表示。 */
export function buildEquivalentPayload(graph: CompiledGraph): EquivalentPayload {
  const json = JSON.stringify({
    sid: '9007199254740993',
    profile: {
      dn: 'Ada',
      score: 42,
      delegates: [{ displayName: 'Grace', score: -7 }],
    },
    tags: ['a', 'b'],
    active: true,
    sameNumberEverywhere: 12,
  });
  const binary = encodeRootMessage(graph, {
    1: 9007199254740993n,
    2: {
      10: 'Ada',
      11: 42,
      12: [{ 10: 'Grace', 11: -7 }],
    },
    3: ['a', 'b'],
    4: true,
    5: 12,
  });
  return { json, binary };
}

/** 枚举 [0, length] 的全部字节切点。 */
export function allCuts(length: number): number[] {
  return Array.from({ length: length + 1 }, (_, index) => index);
}

export function slices(data: Uint8Array, at: number): [Uint8Array, Uint8Array] {
  return [data.subarray(0, at), data.subarray(at)];
}

export function utf8Bytes(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}
