import {
  ByteReader,
  ThriftDecodeError,
  ThriftMessageType,
  ThriftType,
  isValidWireType,
  minimumWireSize,
  utf8Decode,
} from './protocol.ts';
import {
  lookupSpec,
  type DecodedThriftMessage,
  type DecodedThriftStruct,
  type FieldSpec,
  type FieldTable,
  type StrictDecodeOptions,
  type ThriftField,
  type ThriftValue,
} from './types.ts';
import { encodeMessage, encodeStruct } from './encoder.ts';

const DEFAULT_MAX_DEPTH = 16;
const DEFAULT_MAX_UNKNOWN_BYTES = 64 * 1024;
const DEFAULT_MAX_MESSAGE_BYTES = 16 * 1024 * 1024;
const STRICT_MESSAGE_VERSION = 0x80010000;

interface DecodeContext {
  maxDepth: number;
  maxUnknownBytes: number;
  unknownBytes: number;
}

function resolveOptions(options: StrictDecodeOptions = {}): {
  maxDepth: number;
  maxUnknownBytes: number;
  maxMessageBytes: number;
} {
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  const maxUnknownBytes = options.maxUnknownBytes ?? DEFAULT_MAX_UNKNOWN_BYTES;
  const maxMessageBytes = options.maxMessageBytes ?? DEFAULT_MAX_MESSAGE_BYTES;
  if (maxDepth < 1 || maxUnknownBytes < 0 || maxMessageBytes < 1) {
    throw new ThriftDecodeError('INVALID_MESSAGE', 'invalid strict decode options', 0);
  }
  return { maxDepth, maxUnknownBytes, maxMessageBytes };
}

/** 严格解码一整条 strict binary 消息，并返回可直接转发的规范重编码字节。 */
export function decodeMessage(
  bytes: Uint8Array,
  fieldTable: FieldTable,
  options: StrictDecodeOptions = {},
): DecodedThriftMessage {
  const limits = resolveOptions(options);
  if (bytes.length > limits.maxMessageBytes) {
    throw new ThriftDecodeError(
      'MESSAGE_TOO_LARGE',
      `message is ${bytes.length} byte(s), limit is ${limits.maxMessageBytes}`,
      bytes.length,
    );
  }

  const reader = new ByteReader(bytes);
  // 版本前缀最高位为 1，必须按无符号 32 位读取再比较。
  const versionAndType = reader.readU32();
  // 用无符号右移取高 16 位，避免 32 位位运算把 0x8001xxxx 变成负数。
  if ((versionAndType >>> 16) !== (STRICT_MESSAGE_VERSION >>> 16)) {
    throw new ThriftDecodeError(
      'INVALID_MESSAGE',
      'only strict binary protocol (version 1) messages are accepted',
      0,
    );
  }

  // 高 16 位为 strict 版本 1，低字节为消息类型。
  const messageType = versionAndType & 0xff;
  if (
    messageType !== ThriftMessageType.CALL &&
    messageType !== ThriftMessageType.REPLY &&
    messageType !== ThriftMessageType.EXCEPTION &&
    messageType !== ThriftMessageType.ONEWAY
  ) {
    throw new ThriftDecodeError('INVALID_MESSAGE', `unknown message type ${messageType}`, reader.offset);
  }

  const nameLength = reader.readI32();
  if (nameLength < 0) {
    throw new ThriftDecodeError('INVALID_LENGTH', 'negative method name length', reader.offset);
  }
  const methodName = utf8Decode(reader.readRaw(nameLength));
  const sequenceId = reader.readI32();

  const context: DecodeContext = {
    maxDepth: limits.maxDepth,
    maxUnknownBytes: limits.maxUnknownBytes,
    unknownBytes: 0,
  };
  const root = readStruct(reader, fieldTable, 1, context, true);
  if (reader.remaining !== 0) {
    throw new ThriftDecodeError('TRAILING_BYTES', `${reader.remaining} byte(s) after root struct STOP`, reader.offset);
  }

  const canonical = encodeMessage(methodName, messageType, sequenceId, root);
  return {
    methodName,
    messageType,
    sequenceId,
    root,
    canonical,
    unknownBytes: context.unknownBytes,
  };

}

/** 只解码结构体载荷（不含消息 envelope），用于网关已自行拆包的场景。 */
export function decodeStructBytes(
  bytes: Uint8Array,
  fieldTable: FieldTable,
  options: StrictDecodeOptions = {},
): DecodedThriftStruct {
  const limits = resolveOptions(options);
  if (bytes.length > limits.maxMessageBytes) {
    throw new ThriftDecodeError('MESSAGE_TOO_LARGE', 'struct payload exceeds size limit', bytes.length);
  }
  const reader = new ByteReader(bytes);
  const context: DecodeContext = {
    maxDepth: limits.maxDepth,
    maxUnknownBytes: limits.maxUnknownBytes,
    unknownBytes: 0,
  };
  const root = readStruct(reader, fieldTable, 1, context, true);
  if (reader.remaining !== 0) {
    throw new ThriftDecodeError('TRAILING_BYTES', `${reader.remaining} byte(s) after struct STOP`, reader.offset);
  }
  return { root, canonical: encodeStruct(root), unknownBytes: context.unknownBytes };
}

function readStruct(
  reader: ByteReader,
  table: FieldTable | undefined,
  depth: number,
  context: DecodeContext,
  chargeUnknown: boolean,
): ThriftValue {
  if (depth > context.maxDepth) {
    throw new ThriftDecodeError('NESTED_TOO_DEEP', `struct nesting exceeds depth ${context.maxDepth}`, reader.offset);
  }

  const fields: ThriftField[] = [];
  const seenIds = new Set<number>();

  for (;;) {
    const fieldType = reader.readByte();
    if (fieldType === ThriftType.STOP) {
      return { type: ThriftType.STRUCT, fields };
    }
    if (!isValidWireType(fieldType)) {
      throw new ThriftDecodeError('INVALID_TYPE', `invalid field wire type ${fieldType}`, reader.offset - 1);
    }

    const fieldId = reader.readI16();
    if (fieldId <= 0) {
      throw new ThriftDecodeError('INVALID_FIELD_ID', `field id must be positive, got ${fieldId}`, reader.offset - 2);
    }
    if (seenIds.has(fieldId)) {
      throw new ThriftDecodeError('DUPLICATE_FIELD', `field id ${fieldId} appears more than once`, reader.offset);
    }
    seenIds.add(fieldId);

    const spec = lookupSpec(table, fieldId);
    if (spec && spec.type !== fieldType) {
      // 整体拒绝：不跳过、不猜测，避免网关与后端对同一字段解析分叉。
      throw new ThriftDecodeError(
        'FIELD_TYPE_MISMATCH',
        `field ${fieldId} declared as wire type ${spec.type} but arrived as ${fieldType}`,
        reader.offset,
      );
    }

    const unknownStart = reader.offset;
    // 未知字段按整段原始字节计费一次，因此其内部递归不再重复累加；
    // 已知字段继续沿用上层计费标记，使已知结构体内的未知字段仍被统计。
    const value = readValue(reader, fieldType, spec, depth, context, spec ? chargeUnknown : false);
    if (!spec && chargeUnknown) {
      context.unknownBytes += reader.offset - unknownStart;
      if (context.unknownBytes > context.maxUnknownBytes) {
        throw new ThriftDecodeError(
          'UNKNOWN_BYTES_EXCEEDED',
          `unknown fields total ${context.unknownBytes} byte(s), limit is ${context.maxUnknownBytes}`,
          reader.offset,
        );
      }
    }

    fields.push({ id: fieldId, value, known: Boolean(spec) });
  }
}

function readValue(
  reader: ByteReader,
  type: ThriftType,
  spec: FieldSpec | undefined,
  depth: number,
  context: DecodeContext,
  chargeUnknown: boolean,
): ThriftValue {
  if (spec && spec.type !== type) {
    throw new ThriftDecodeError(
      'FIELD_TYPE_MISMATCH',
      `element declared as wire type ${spec.type} but arrived as ${type}`,
      reader.offset,
    );
  }

  switch (type) {
    case ThriftType.BOOL: {
      const raw = reader.readByte();
      if (raw !== 0 && raw !== 1) {
        throw new ThriftDecodeError('INVALID_TYPE', `bool wire value must be 0 or 1, got ${raw}`, reader.offset - 1);
      }
      return { type, value: raw === 1 };
    }
    case ThriftType.BYTE:
      // 有符号 i8。
      return { type, value: (reader.readByte() << 24) >> 24 };
    case ThriftType.I16:
      return { type, value: reader.readI16() };
    case ThriftType.I32:
      return { type, value: reader.readI32() };
    case ThriftType.I64:
      return { type, value: reader.readI64() };
    case ThriftType.DOUBLE:
      return { type, value: reader.readDouble() };
    case ThriftType.STRING: {
      const length = reader.readI32();
      if (length < 0) {
        throw new ThriftDecodeError('INVALID_LENGTH', `negative string/binary length ${length}`, reader.offset);
      }
      return { type, value: reader.readRaw(length) };
    }
    case ThriftType.STRUCT:
      if (depth + 1 > context.maxDepth) {
        throw new ThriftDecodeError('NESTED_TOO_DEEP', `nesting exceeds depth ${context.maxDepth}`, reader.offset);
      }
      return readStruct(reader, spec?.fields, depth + 1, context, chargeUnknown);
    case ThriftType.LIST:
    case ThriftType.SET:
      return readSequence(reader, type, spec, depth, context, chargeUnknown);
    case ThriftType.MAP:
      return readMap(reader, spec, depth, context, chargeUnknown);
    default:
      throw new ThriftDecodeError('INVALID_TYPE', `unsupported wire type ${type}`, reader.offset);
  }
}

function assertNested(depth: number, context: DecodeContext, offset: number): void {
  if (depth + 1 > context.maxDepth) {
    throw new ThriftDecodeError('NESTED_TOO_DEEP', `nesting exceeds depth ${context.maxDepth}`, offset);
  }
}

function checkDeclaredSize(size: number, minElementBytes: number, remaining: number, offset: number): void {
  if (size < 0) {
    throw new ThriftDecodeError('INVALID_LENGTH', `negative container length ${size}`, offset);
  }
  // 按剩余字节能容纳的“最小元素数”预校验，夸大长度在任何内存分配前失败。
  if (size > Number.MAX_SAFE_INTEGER || minElementBytes * size > remaining) {
    throw new ThriftDecodeError(
      'CONTAINER_LENGTH_TOO_LARGE',
      `declared length ${size} needs at least ${minElementBytes * size} byte(s), only ${remaining} left`,
      offset,
    );
  }
}

function readSequence(
  reader: ByteReader,
  type: ThriftType.LIST | ThriftType.SET,
  spec: FieldSpec | undefined,
  depth: number,
  context: DecodeContext,
  chargeUnknown: boolean,
): ThriftValue {
  const elementType = reader.readByte();
  if (!isValidWireType(elementType)) {
    throw new ThriftDecodeError('INVALID_TYPE', `invalid ${type === ThriftType.LIST ? 'list' : 'set'} element type ${elementType}`, reader.offset - 1);
  }
  if (spec?.element && spec.element.type !== elementType) {
    throw new ThriftDecodeError(
      'FIELD_TYPE_MISMATCH',
      `container element declared as ${spec.element.type} but arrived as ${elementType}`,
      reader.offset,
    );
  }
  const size = reader.readI32();
  checkDeclaredSize(size, minimumWireSize(elementType), reader.remaining, reader.offset);
  assertNested(depth, context, reader.offset);

  const items: ThriftValue[] = new Array(size);
  for (let index = 0; index < size; index += 1) {
    items[index] = readValue(reader, elementType, spec?.element, depth + 1, context, chargeUnknown);
  }
  return { type, elementType, items };
}

function readMap(
  reader: ByteReader,
  spec: FieldSpec | undefined,
  depth: number,
  context: DecodeContext,
  chargeUnknown: boolean,
): ThriftValue {
  const keyType = reader.readByte();
  const valueType = reader.readByte();
  if (!isValidWireType(keyType) || !isValidWireType(valueType)) {
    throw new ThriftDecodeError(
      'INVALID_TYPE',
      `invalid map entry types key=${keyType} value=${valueType}`,
      reader.offset - 2,
    );
  }
  if (spec?.key && spec.key.type !== keyType) {
    throw new ThriftDecodeError('FIELD_TYPE_MISMATCH', `map key declared as ${spec.key.type} but arrived as ${keyType}`, reader.offset);
  }
  if (spec?.value && spec.value.type !== valueType) {
    throw new ThriftDecodeError('FIELD_TYPE_MISMATCH', `map value declared as ${spec.value.type} but arrived as ${valueType}`, reader.offset);
  }

  const size = reader.readI32();
  checkDeclaredSize(size, minimumWireSize(keyType) + minimumWireSize(valueType), reader.remaining, reader.offset);
  assertNested(depth, context, reader.offset);

  const entries = new Array(size);
  for (let index = 0; index < size; index += 1) {
    const key = readValue(reader, keyType, spec?.key, depth + 1, context, chargeUnknown);
    const value = readValue(reader, valueType, spec?.value, depth + 1, context, chargeUnknown);
    entries[index] = { key, value };
  }
  return { type: ThriftType.MAP, keyType, valueType, entries };
}
