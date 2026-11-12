import type { ThriftType } from './protocol.ts';

/**
 * 解码后的规范值树。容器/结构体统一走值树，保证可以无损、确定地重新编码。
 * 字符串与二进制在线上同为 STRING 类型，统一保留原始字节，避免 UTF-8 归一化差异；
 * 需要文本时由调用方自行 utf8Decode。
 */
export type ThriftValue =
  | { type: ThriftType.BOOL; value: boolean }
  | { type: ThriftType.BYTE; value: number }
  | { type: ThriftType.I16; value: number }
  | { type: ThriftType.I32; value: number }
  | { type: ThriftType.I64; value: bigint }
  | { type: ThriftType.DOUBLE; value: number }
  | { type: ThriftType.STRING; value: Uint8Array }
  | { type: ThriftType.STRUCT; fields: ThriftField[] }
  | { type: ThriftType.LIST; elementType: ThriftType; items: ThriftValue[] }
  | { type: ThriftType.SET; elementType: ThriftType; items: ThriftValue[] }
  | { type: ThriftType.MAP; keyType: ThriftType; valueType: ThriftType; entries: MapEntry[] };

export interface ThriftField {
  id: number;
  value: ThriftValue;
  /** 字段表中存在则为 true；仅用于网关取已知字段，重编码不依赖它。 */
  known: boolean;
}

export interface MapEntry {
  key: ThriftValue;
  value: ThriftValue;
}

/**
 * 调用方提供的字段表。type 是该字段唯一允许的线上类型，
 * 嵌套结构/容器元素再给一张表或元素规格，用来递归做同样的严格校验。
 */
export interface FieldSpec {
  type: ThriftType;
  /** type 为 STRUCT 时的嵌套字段表。 */
  fields?: FieldTable;
  /** type 为 LIST/SET 时的元素规格；给了就校验元素线上类型。 */
  element?: FieldSpec;
  /** type 为 MAP 时的键/值规格。 */
  key?: FieldSpec;
  value?: FieldSpec;
}

/** 字段 id -> 规格，id 必须为正整数。 */
export type FieldTable = ReadonlyMap<number, FieldSpec> | Readonly<Record<number, FieldSpec>>;

export interface StrictDecodeOptions {
  /** 结构体与容器的最大嵌套深度（根结构体深度为 1），默认 16。 */
  maxDepth?: number;
  /** 所有未知字段原始字节总和上限，默认 64 KiB。 */
  maxUnknownBytes?: number;
  /** 整条消息字节上限，默认 16 MiB。 */
  maxMessageBytes?: number;
}

export interface DecodedThriftMessage {
  methodName: string;
  messageType: import('./protocol.ts').ThriftMessageType;
  sequenceId: number;
  root: ThriftValue;
  /** 规范重编码后的完整消息字节，网关直接转发它。 */
  canonical: Uint8Array;
  unknownBytes: number;
}

export interface DecodedThriftStruct {
  root: ThriftValue;
  /** 仅结构体本身的规范字节（含 STOP，不含消息 envelope）。 */
  canonical: Uint8Array;
  unknownBytes: number;
}

export function lookupSpec(table: FieldTable | undefined, id: number): FieldSpec | undefined {
  if (!table) {
    return undefined;
  }
  if (table instanceof Map) {
    return table.get(id);
  }
  return table[id];
}
