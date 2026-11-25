import { createHash } from 'node:crypto';
import type { CompiledGraph } from './schema.ts';

/**
 * 授权身份树。只包含通过 schema 校验的已知字段；被跳过的未知字段
 * 不会出现在这里。对象节点的 fields 最终按字段编号排序。
 */
export type IdNode =
  | { kind: 'object'; typeNo: number; fields: IdField[] }
  | { kind: 'array'; typeNo: number; elements: IdNode[] }
  | { kind: 'id64'; typeNo: number; value: string }
  | { kind: 'int32'; typeNo: number; value: number }
  | { kind: 'string'; typeNo: number; value: string }
  | { kind: 'bool'; typeNo: number; value: boolean };

export interface IdField {
  no: number;
  value: IdNode;
}

const textEncoder = new TextEncoder();

export function makeObject(typeNo: number): IdNode {
  return { kind: 'object', typeNo, fields: [] };
}

export function makeArray(typeNo: number): IdNode {
  return { kind: 'array', typeNo, elements: [] };
}

/** 按字段编号排序；嵌套对象递归排序，得到稳定的最小表示。 */
export function sortIdentity(node: IdNode): void {
  if (node.kind === 'object') {
    node.fields.sort((a, b) => a.no - b.no);
    for (const field of node.fields) {
      sortIdentity(field.value);
    }
  } else if (node.kind === 'array') {
    for (const element of node.elements) {
      sortIdentity(element);
    }
  }
}

/**
 * 最小规范编码（TLV，全部整数网络序）：
 *   对象: 0x01 | u32 typeNo | u32 count | (u32 fieldNo | node)*  按编号排序
 *   数组: 0x02 | u32 typeNo | u32 count | node*
 *   id64: 0x03 | u32 typeNo | len-prefixed 十进制 UTF-8
 *   i32 : 0x04 | u32 typeNo | i32 值（4 字节）
 *   串 : 0x05 | u32 typeNo | len-prefixed UTF-8
 *   布尔: 0x06 | u32 typeNo | 1 字节
 * 长度前缀均为 u32。
 */
export function encodeMinimal(node: IdNode): Uint8Array {
  const parts: Uint8Array[] = [];
  encodeNode(parts, node);
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

function encodeNode(parts: Uint8Array[], node: IdNode): void {
  parts.push(Uint8Array.of(tagFor(node.kind)));
  putU32(parts, node.typeNo);
  switch (node.kind) {
    case 'object': {
      putU32(parts, node.fields.length);
      for (const field of node.fields) {
        putU32(parts, field.no);
        encodeNode(parts, field.value);
      }
      break;
    }
    case 'array': {
      putU32(parts, node.elements.length);
      for (const element of node.elements) {
        encodeNode(parts, element);
      }
      break;
    }
    case 'id64':
      putBytes(parts, textEncoder.encode(node.value));
      break;
    case 'int32': {
      const buffer = new ArrayBuffer(4);
      new DataView(buffer).setInt32(0, node.value, false);
      parts.push(new Uint8Array(buffer));
      break;
    }
    case 'string':
      putBytes(parts, textEncoder.encode(node.value));
      break;
    case 'bool':
      parts.push(Uint8Array.of(node.value ? 1 : 0));
      break;
  }
}

function tagFor(kind: IdNode['kind']): number {
  switch (kind) {
    case 'object':
      return 0x01;
    case 'array':
      return 0x02;
    case 'id64':
      return 0x03;
    case 'int32':
      return 0x04;
    case 'string':
      return 0x05;
    case 'bool':
      return 0x06;
  }
}

function putU32(parts: Uint8Array[], value: number): void {
  const buffer = new ArrayBuffer(4);
  new DataView(buffer).setUint32(0, value, false);
  parts.push(new Uint8Array(buffer));
}

function putBytes(parts: Uint8Array[], bytes: Uint8Array): void {
  putU32(parts, bytes.length);
  parts.push(bytes);
}

/**
 * 签名键 = SHA-256( len(tenant) | tenant | u32 version | u32 rootTypeNo
 *                  | len(minimal) | minimal )，十六进制输出。
 * 租户、类型版本与按编号排序的最小编码全部绑定在摘要里。
 */
export function buildSignatureKey(
  graph: CompiledGraph,
  tenant: string,
  minimal: Uint8Array,
): string {
  const parts: Uint8Array[] = [];
  putBytes(parts, textEncoder.encode(tenant));
  putU32(parts, graph.version);
  putU32(parts, graph.rootTypeNo);
  putBytes(parts, minimal);
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const material = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    material.set(part, offset);
    offset += part.length;
  }
  return createHash('sha256').update(material).digest('hex');
}
