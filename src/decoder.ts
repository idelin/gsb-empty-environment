/**
 * Reflection-style schema validation engine + zero-allocation decoder.
 *
 * Decoded message objects and repeated-field arrays come from per-schema
 * object pools; the WireReader is reused across decodes, so a steady-state
 * decode of a previously-seen schema performs no message/array allocations.
 * Callers must release() every successfully decoded message back.
 */
import { DecodeError, TruncatedError, WireReader, WireType } from './wire.ts';
import { ObjectPool } from './pool.ts';

export type ScalarType =
  | 'varint'
  | 'zigzag32'
  | 'zigzag64'
  | 'fixed32'
  | 'fixed64'
  | 'bool'
  | 'string'
  | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldDef {
  readonly no: number;
  readonly name: string;
  readonly type: FieldType;
  readonly repeated?: boolean;
  readonly packed?: boolean;
  readonly schema?: Schema;
}

export interface Schema {
  readonly name: string;
  readonly fields: readonly FieldDef[];
  readonly byNo: readonly (FieldDef | undefined)[];
}

const PACKABLE: ReadonlySet<FieldType> = new Set([
  'varint',
  'zigzag32',
  'zigzag64',
  'fixed32',
  'fixed64',
  'bool',
]);

const EXPECTED_WIRE: Record<FieldType, number> = {
  varint: WireType.Varint,
  zigzag32: WireType.Varint,
  zigzag64: WireType.Varint,
  bool: WireType.Varint,
  fixed64: WireType.Fixed64,
  string: WireType.LengthDelimited,
  bytes: WireType.LengthDelimited,
  message: WireType.LengthDelimited,
  fixed32: WireType.Fixed32,
};

export function defineSchema(name: string, fields: readonly FieldDef[]): Schema {
  if (fields.length === 0) throw new DecodeError(`schema ${name}: no fields defined`);
  const byNo: (FieldDef | undefined)[] = [];
  for (const def of fields) {
    if (!Number.isInteger(def.no) || def.no < 1 || def.no > 0x1fff_ffff) {
      throw new DecodeError(`schema ${name}.${def.name}: invalid field number ${def.no}`);
    }
    if (byNo[def.no] !== undefined) {
      throw new DecodeError(`schema ${name}: duplicate field number ${def.no}`);
    }
    if (def.type === 'message' && def.schema === undefined) {
      throw new DecodeError(`schema ${name}.${def.name}: message field requires nested schema`);
    }
    if (def.packed) {
      if (!def.repeated) {
        throw new DecodeError(`schema ${name}.${def.name}: packed requires repeated`);
      }
      if (!PACKABLE.has(def.type)) {
        throw new DecodeError(`schema ${name}.${def.name}: type ${def.type} is not packable`);
      }
    }
    byNo[def.no] = def;
  }
  return { name, fields, byNo };
}

type Message = Record<string, unknown>;

export class MessageDecoder {
  private readonly reader = new WireReader();
  private readonly messagePools = new Map<Schema, ObjectPool<Message>>();
  private readonly arrayPool: ObjectPool<unknown[]>;
  readonly root: Schema;

  constructor(root: Schema, poolCapacity = 1024) {
    this.root = root;
    this.arrayPool = new ObjectPool<unknown[]>(
      () => [],
      (arr) => {
        arr.length = 0;
      },
      poolCapacity,
    );
  }

  private poolFor(schema: Schema): ObjectPool<Message> {
    let pool = this.messagePools.get(schema);
    if (pool === undefined) {
      pool = new ObjectPool<Message>(() => ({}), () => undefined, 1024);
      this.messagePools.set(schema, pool);
    }
    return pool;
  }

  decode(buf: Uint8Array): Message {
    const msg = this.poolFor(this.root).acquire();
    this.reader.reset(buf);
    try {
      this.decodeMessage(this.root, msg);
    } catch (err) {
      this.release(msg);
      throw err;
    }
    return msg;
  }

  private decodeMessage(schema: Schema, msg: Message): void {
    const reader = this.reader;
    while (!reader.eof()) {
      const tag = reader.readTag();
      const wire = tag & 7;
      const def = schema.byNo[Math.floor(tag / 8)];
      if (def === undefined) {
        reader.skipField(wire);
        continue;
      }
      if (def.packed && wire === WireType.LengthDelimited) {
        const arr = this.acquireArray(msg, def);
        reader.pushLimit(reader.readLength());
        while (!reader.eof()) {
          arr.push(this.readScalar(def));
        }
        reader.popLimit();
        continue;
      }
      const expected = EXPECTED_WIRE[def.type];
      if (wire !== expected) {
        throw new DecodeError(
          `schema ${schema.name}.${def.name}: wire type ${wire} does not match expected ${expected}`,
        );
      }
      if (def.repeated) {
        this.acquireArray(msg, def).push(this.readValue(def));
      } else {
        msg[def.name] = this.readValue(def);
      }
    }
  }

  private acquireArray(msg: Message, def: FieldDef): unknown[] {
    let arr = msg[def.name] as unknown[] | undefined;
    if (arr === undefined) {
      arr = this.arrayPool.acquire();
      msg[def.name] = arr;
    }
    return arr;
  }

  private readScalar(def: FieldDef): unknown {
    const reader = this.reader;
    switch (def.type) {
      case 'varint':
        return reader.readVarint();
      case 'zigzag32':
        return reader.readZigZag32();
      case 'zigzag64':
        return reader.readZigZag64();
      case 'fixed32':
        return reader.readFixed32();
      case 'fixed64':
        return reader.readFixed64();
      case 'bool':
        return reader.readBool();
      case 'string':
        return reader.readString();
      case 'bytes':
        return reader.readBytes();
      default:
        throw new DecodeError(`field ${def.name}: type ${def.type} is not a scalar`);
    }
  }

  private readValue(def: FieldDef): unknown {
    if (def.type !== 'message') {
      return this.readScalar(def);
    }
    const childSchema = def.schema as Schema;
    this.reader.pushLimit(this.reader.readLength());
    const child = this.poolFor(childSchema).acquire();
    try {
      this.decodeMessage(childSchema, child);
      this.reader.popLimit();
    } catch (err) {
      this.reader.abortToDepth();
      this.releaseMessage(childSchema, child);
      throw err;
    }
    return child;
  }

  /** Recursively returns a decoded message graph to the pools. */
  release(msg: Message): void {
    this.releaseMessage(this.root, msg);
  }

  private releaseMessage(schema: Schema, msg: Message): void {
    for (const def of schema.fields) {
      const value = msg[def.name];
      if (value === undefined) continue;
      if (def.type === 'message') {
        const childSchema = def.schema as Schema;
        if (def.repeated) {
          for (const child of value as Message[]) {
            this.releaseMessage(childSchema, child);
          }
          this.arrayPool.release(value as unknown[]);
        } else {
          this.releaseMessage(childSchema, value as Message);
        }
      } else if (def.repeated) {
        this.arrayPool.release(value as unknown[]);
      }
      delete msg[def.name];
    }
    this.poolFor(schema).release(msg);
  }

  assertNoLeaks(): void {
    for (const [schema, pool] of this.messagePools) {
      pool.assertNoLeaks(`message pool(${schema.name})`);
    }
    this.arrayPool.assertNoLeaks('array pool');
  }

  get stats(): Record<string, unknown> {
    const messages: Record<string, unknown> = {};
    for (const [schema, pool] of this.messagePools) {
      messages[schema.name] = pool.stats;
    }
    return { messages, arrays: this.arrayPool.stats };
  }
}

export { DecodeError, TruncatedError };
