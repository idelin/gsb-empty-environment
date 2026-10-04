import { WireError, DecodeError } from './errors.ts';
import { WireReader, WireType, type WireTypeValue } from './reader.ts';
import { zigzagDecode } from './varint.ts';

export type ScalarType =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'fixed32'
  | 'fixed64'
  | 'sfixed32'
  | 'sfixed64'
  | 'float'
  | 'double'
  | 'string'
  | 'bytes';

export interface MessageSchema {
  readonly name: string;
  readonly fields: Readonly<Record<number, FieldDef>>;
}

export interface FieldDef {
  readonly name: string;
  readonly type: ScalarType | MessageSchema;
  readonly repeated?: boolean;
  readonly packed?: boolean;
}

export interface DecodeOptions {
  readonly strict?: boolean;
  readonly maxDepth?: number;
}

export interface ValidationResult {
  readonly ok: boolean;
  readonly errors: readonly string[];
}

const DEFAULT_MAX_DEPTH = 100;

const WIRE_TYPE_OF: Record<ScalarType, WireTypeValue> = {
  int32: WireType.Varint,
  int64: WireType.Varint,
  uint32: WireType.Varint,
  uint64: WireType.Varint,
  sint32: WireType.Varint,
  sint64: WireType.Varint,
  bool: WireType.Varint,
  fixed64: WireType.Fixed64,
  sfixed64: WireType.Fixed64,
  double: WireType.Fixed64,
  fixed32: WireType.Fixed32,
  sfixed32: WireType.Fixed32,
  float: WireType.Fixed32,
  string: WireType.LengthDelimited,
  bytes: WireType.LengthDelimited,
};

const PACKABLE: ReadonlySet<ScalarType> = new Set<ScalarType>([
  'int32',
  'int64',
  'uint32',
  'uint64',
  'sint32',
  'sint64',
  'bool',
  'fixed32',
  'fixed64',
  'sfixed32',
  'sfixed64',
  'float',
  'double',
]);

const utf8Decoder = new TextDecoder('utf-8', { fatal: true });

function isMessageSchema(type: ScalarType | MessageSchema): type is MessageSchema {
  return typeof type === 'object';
}

function readScalar(reader: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32':
      return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'int64':
      return BigInt.asIntN(64, reader.readVarint());
    case 'uint32':
      return Number(BigInt.asUintN(32, reader.readVarint()));
    case 'uint64':
      return reader.readVarint();
    case 'sint32':
      return Number(BigInt.asIntN(32, zigzagDecode(reader.readVarint())));
    case 'sint64':
      return zigzagDecode(reader.readVarint());
    case 'bool':
      return reader.readVarint() !== 0n;
    case 'fixed32':
      return reader.readFixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed32':
      return Number(BigInt.asIntN(32, BigInt(reader.readFixed32())));
    case 'sfixed64':
      return BigInt.asIntN(64, reader.readFixed64());
    case 'float':
      return reader.readFloat();
    case 'double':
      return reader.readDouble();
    case 'string': {
      const bytes = reader.readLengthDelimited();
      try {
        return utf8Decoder.decode(bytes);
      } catch {
        throw new WireError('string 字段包含非法 UTF-8 序列', reader.offset);
      }
    }
    case 'bytes':
      return reader.readLengthDelimited();
  }
}

function pushField(out: Record<string, unknown>, name: string, value: unknown): void {
  const existing = out[name];
  if (existing === undefined) {
    out[name] = [value];
  } else {
    (existing as unknown[]).push(value);
  }
}

function decodeField(
  reader: WireReader,
  def: FieldDef,
  wireType: WireTypeValue,
  out: Record<string, unknown>,
  options: DecodeOptions,
  depth: number,
  path: string,
): void {
  const type = def.type;

  if (isMessageSchema(type)) {
    if (wireType !== WireType.LengthDelimited) {
      throw new DecodeError(path, 'wire type ' + wireType + ' 与嵌套消息不匹配（期望 2）');
    }
    const sub = reader.readLengthDelimited();
    const value = decodeInner(type, new WireReader(sub), options, depth + 1, path);
    if (def.repeated) pushField(out, def.name, value);
    else out[def.name] = value;
    return;
  }

  if (def.repeated && def.packed && wireType === WireType.LengthDelimited) {
    if (!PACKABLE.has(type)) {
      throw new DecodeError(path, '类型 ' + type + ' 不支持 packed 编码');
    }
    const sub = reader.readLengthDelimited();
    const subReader = new WireReader(sub);
    while (!subReader.done) {
      pushField(out, def.name, readScalar(subReader, type));
    }
    return;
  }

  const expected = WIRE_TYPE_OF[type];
  if (wireType !== expected) {
    throw new DecodeError(
      path,
      'wire type ' + wireType + ' 与字段类型 ' + type + ' 不匹配（期望 ' + expected + '）',
    );
  }
  const value = readScalar(reader, type);
  if (def.repeated) pushField(out, def.name, value);
  else out[def.name] = value;
}

function decodeInner(
  schema: MessageSchema,
  reader: WireReader,
  options: DecodeOptions,
  depth: number,
  path: string,
): Record<string, unknown> {
  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  if (depth > maxDepth) {
    throw new DecodeError(path, '嵌套深度超过上限 ' + maxDepth + '，资源保护已触发');
  }
  const out: Record<string, unknown> = {};
  while (!reader.done) {
    let tag;
    try {
      tag = reader.readTag();
    } catch (err) {
      if (err instanceof WireError) throw new DecodeError(path, err.message);
      throw err;
    }
    const def = schema.fields[tag.field];
    if (!def) {
      if (options.strict) {
        throw new DecodeError(path, '未知字段号 ' + tag.field + '（strict 模式）');
      }
      try {
        reader.skip(tag.wireType);
      } catch (err) {
        if (err instanceof WireError) throw new DecodeError(path, err.message);
        throw err;
      }
      continue;
    }
    const fieldPath = path + '.' + def.name;
    try {
      decodeField(reader, def, tag.wireType, out, options, depth, fieldPath);
    } catch (err) {
      if (err instanceof WireError) throw new DecodeError(fieldPath, err.message);
      throw err;
    }
  }
  return out;
}

export function decodeMessage(
  schema: MessageSchema,
  buf: Uint8Array,
  options: DecodeOptions = {},
): Record<string, unknown> {
  return decodeInner(schema, new WireReader(buf), options, 0, schema.name);
}

export function validateMessage(
  schema: MessageSchema,
  buf: Uint8Array,
  options: DecodeOptions = {},
): ValidationResult {
  try {
    decodeMessage(schema, buf, options);
    return { ok: true, errors: [] };
  } catch (err) {
    return { ok: false, errors: [err instanceof Error ? err.message : String(err)] };
  }
}
