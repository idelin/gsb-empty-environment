import {
  ByteWriter,
  ThriftMessageType,
  ThriftType,
  utf8Encode,
} from './protocol.ts';
import type { ThriftField, ThriftValue } from './types.ts';

const STRICT_MESSAGE_VERSION = 0x80010000;

function writeValue(writer: ByteWriter, value: ThriftValue): void {
  switch (value.type) {
    case ThriftType.BOOL:
      // 规范形式：bool 线值只允许 0/1。
      writer.writeByte(value.value ? 1 : 0);
      return;
    case ThriftType.BYTE:
      writer.writeByte(value.value);
      return;
    case ThriftType.I16:
      writer.writeI16(value.value);
      return;
    case ThriftType.I32:
      writer.writeI32(value.value);
      return;
    case ThriftType.I64:
      writer.writeI64(value.value);
      return;
    case ThriftType.DOUBLE:
      writer.writeDouble(value.value);
      return;
    case ThriftType.STRING:
      writer.writeI32(value.value.length);
      writer.writeBytes(value.value);
      return;
    case ThriftType.STRUCT:
      writeStruct(writer, value.fields);
      return;
    case ThriftType.LIST:
    case ThriftType.SET:
      writer.writeByte(value.elementType);
      writer.writeI32(value.items.length);
      for (const item of value.items) {
        writeValue(writer, item);
      }
      return;
    case ThriftType.MAP: {
      writer.writeByte(value.keyType);
      writer.writeByte(value.valueType);
      writer.writeI32(value.entries.length);
      for (const entry of value.entries) {
        writeValue(writer, entry.key);
        writeValue(writer, entry.value);
      }
      return;
    }
  }
}

function writeStruct(writer: ByteWriter, fields: readonly ThriftField[]): void {
  // 规范形式：字段按 id 升序，重复字段在解码阶段已被拒绝。
  const ordered = [...fields].sort((left, right) => left.id - right.id);
  for (const field of ordered) {
    writer.writeByte(field.value.type);
    writer.writeI16(field.id);
    writeValue(writer, field.value);
  }
  writer.writeByte(ThriftType.STOP);
}

/** 仅编码结构体载荷（含 STOP）。 */
export function encodeStruct(root: ThriftValue): Uint8Array {
  if (root.type !== ThriftType.STRUCT) {
    throw new TypeError('encodeStruct expects a STRUCT value tree root');
  }
  const writer = new ByteWriter();
  writeStruct(writer, root.fields);
  return writer.toBytes();
}

/** 编码为 strict binary 消息字节，供网关转发后端。 */
export function encodeMessage(
  methodName: string,
  messageType: ThriftMessageType,
  sequenceId: number,
  root: ThriftValue,
): Uint8Array {
  if (root.type !== ThriftType.STRUCT) {
    throw new TypeError('encodeMessage expects a STRUCT value tree root');
  }
  const writer = new ByteWriter();
  writer.writeI32(STRICT_MESSAGE_VERSION | messageType);
  const nameBytes = utf8Encode(methodName);
  writer.writeI32(nameBytes.length);
  writer.writeBytes(nameBytes);
  writer.writeI32(sequenceId);
  writeStruct(writer, root.fields);
  return writer.toBytes();
}
