export { decodeMessage, decodeStructBytes } from './decoder.ts';
export { encodeMessage, encodeStruct } from './encoder.ts';
export {
  ByteReader,
  ByteWriter,
  ThriftDecodeError,
  ThriftMessageType,
  ThriftType,
  utf8Decode,
  utf8Encode,
} from './protocol.ts';
export { lookupSpec } from './types.ts';
export type {
  DecodedThriftMessage,
  DecodedThriftStruct,
  FieldSpec,
  FieldTable,
  MapEntry,
  StrictDecodeOptions,
  ThriftField,
  ThriftValue,
} from './types.ts';

import { ThriftType } from './protocol.ts';
import type { ThriftField, ThriftValue } from './types.ts';

function findField(fields: readonly ThriftField[], id: number): ThriftField | undefined {
  return fields.find((field) => field.id === id);
}

/** 按字段 id 取解码后的已知字段值；字段缺失或类型不符返回 undefined。 */
export function getField(root: ThriftValue, id: number): ThriftField | undefined {
  if (root.type !== ThriftType.STRUCT) {
    return undefined;
  }
  return findField(root.fields, id);
}

/** 网关取租户字段的典型入口：要求字段存在、已知且为指定线上类型。 */
export function getRequiredField(root: ThriftValue, id: number): ThriftField {
  if (root.type !== ThriftType.STRUCT) {
    throw new TypeError('getRequiredField expects a STRUCT value tree root');
  }
  const field = findField(root.fields, id);
  if (!field) {
    throw new Error(`required field ${id} is missing`);
  }
  if (!field.known) {
    throw new Error(`field ${id} is not in the gateway field table`);
  }
  return field;
}
