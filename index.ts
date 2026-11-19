/**
 * V8 序列化载荷预检（preflight）。
 *
 * 在消息入口对不可信客户端数据调用，只扫描字节、绝不还原对象；
 * 通过后才允许交给 v8.deserialize。放行的标签集合：
 *   _ undefined / 0 null / T true / F false
 *   I int32（zigzag 变长整数）/ N double（8 字节）
 *   " 单字节字符串（变长长度 + 字节）
 *   o 对象（键值对，后接 { 与属性数）
 *   A 稠密数组（长度、元素、属性，后接 $ 与属性数和长度）
 *   ^ 对象引用（编号，须指向已出现的对象）
 * 其余一律拒绝：宿主对象 \、各类缓冲区、Map、Set、RegExp、BigInt、未知标签。
 */

export const SERIALIZATION_VERSION = 15;
export const MAX_DEPTH = 32;
export const MAX_PROPERTIES = 4096;

export interface PreflightOptions {
  maxDepth?: number;
  maxProperties?: number;
}

export type PreflightErrorCode =
  | "EMPTY_PAYLOAD"
  | "BAD_HEADER"
  | "UNSUPPORTED_VERSION"
  | "UNEXPECTED_EOF"
  | "MALFORMED_VARINT"
  | "FORBIDDEN_TAG"
  | "UNKNOWN_TAG"
  | "DEPTH_EXCEEDED"
  | "TOO_MANY_PROPERTIES"
  | "BAD_REFERENCE"
  | "STRING_OUT_OF_BOUNDS"
  | "COUNT_MISMATCH"
  | "TRAILING_BYTES";

export class PreflightError extends Error {
  readonly code: PreflightErrorCode;
  readonly offset: number;

  constructor(code: PreflightErrorCode, message: string, offset: number) {
    super(message);
    this.name = "PreflightError";
    this.code = code;
    this.offset = offset;
  }
}

export type PreflightResult =
  | { ok: true }
  | { ok: false; code: PreflightErrorCode; message: string; offset: number };

const TAG_VERSION = 0xff;
const TAG_UNDEFINED = 0x5f; // _
const TAG_NULL = 0x30; // 0
const TAG_TRUE = 0x54; // T
const TAG_FALSE = 0x46; // F
const TAG_INT32 = 0x49; // I
const TAG_DOUBLE = 0x4e; // N
const TAG_ONE_BYTE_STRING = 0x22; // "
const TAG_BEGIN_OBJECT = 0x6f; // o
const TAG_END_OBJECT = 0x7b; // {
const TAG_BEGIN_DENSE_ARRAY = 0x41; // A
const TAG_END_DENSE_ARRAY = 0x24; // $
const TAG_OBJECT_REFERENCE = 0x5e; // ^

/** 认识但明确拒绝的标签：宿主对象、缓冲区、Map/Set、RegExp、BigInt 等。 */
const FORBIDDEN_TAGS: ReadonlyMap<number, string> = new Map([
  [0x5c, "host object (\\)"],
  [0x42, "ArrayBuffer (B)"],
  [0x75, "SharedArrayBuffer (u)"],
  [0x56, "ArrayBufferView (V)"],
  [0x3b, "Map begin (;)"],
  [0x3a, "Map end (:)"],
  [0x27, "Set begin (')"],
  [0x2c, "Set end (,)"],
  [0x52, "RegExp (R)"],
  [0x5a, "BigInt (Z)"],
  [0x7a, "BigIntObject (z)"],
  [0x63, "two-byte string (c)"],
  [0x73, "UTF-8 string (s)"],
  [0x44, "Date (D)"],
  [0x53, "StringObject (S)"],
  [0x6e, "NumberObject (n)"],
  [0x79, "TrueObject (y)"],
  [0x78, "FalseObject (x)"],
  [0x77, "WasmModule (w)"],
]);

class Reader {
  private readonly data: Uint8Array;
  private pos = 0;
  private objectCount = 0;
  private propertyCount = 0;
  private readonly maxDepth: number;
  private readonly maxProperties: number;

  constructor(data: Uint8Array, options: PreflightOptions) {
    this.data = data;
    this.maxDepth = options.maxDepth ?? MAX_DEPTH;
    this.maxProperties = options.maxProperties ?? MAX_PROPERTIES;
  }

  scan(): void {
    if (this.data.length === 0) {
      this.fail("EMPTY_PAYLOAD", "payload is empty");
    }
    if (this.data[0] !== TAG_VERSION) {
      this.fail("BAD_HEADER", "payload must start with 0xFF");
    }
    this.pos = 1;
    const version = this.readByte();
    if (version !== SERIALIZATION_VERSION) {
      this.fail(
        "UNSUPPORTED_VERSION",
        `unsupported serialization version ${version}, expected ${SERIALIZATION_VERSION}`,
      );
    }
    this.readValue(0);
    if (this.pos !== this.data.length) {
      this.fail(
        "TRAILING_BYTES",
        `${this.data.length - this.pos} trailing byte(s) after top-level value`,
      );
    }
  }

  private fail(code: PreflightErrorCode, message: string): never {
    throw new PreflightError(code, message, this.pos);
  }

  private peekByte(): number {
    if (this.pos >= this.data.length) {
      this.fail("UNEXPECTED_EOF", "unexpected end of payload");
    }
    return this.data[this.pos];
  }

  private readByte(): number {
    const byte = this.peekByte();
    this.pos += 1;
    return byte;
  }

  /** LEB128 变长整数，最多 5 字节（32 位）。 */
  private readVarint32(): number {
    let value = 0;
    for (let i = 0; i < 5; i += 1) {
      const byte = this.readByte();
      value += (byte & 0x7f) * 2 ** (7 * i);
      if ((byte & 0x80) === 0) {
        return value;
      }
    }
    this.fail("MALFORMED_VARINT", "varint exceeds 5 bytes");
  }

  private addProperties(count: number): void {
    this.propertyCount += count;
    if (this.propertyCount > this.maxProperties) {
      this.fail(
        "TOO_MANY_PROPERTIES",
        `property/element count exceeds ${this.maxProperties}`,
      );
    }
  }

  private enterContainer(): void {
    // 容器在开始时即占用一个引用编号，允许子值回指自身。
    this.objectCount += 1;
  }

  private readValue(depth: number): void {
    const tag = this.readByte();
    switch (tag) {
      case TAG_UNDEFINED:
      case TAG_NULL:
      case TAG_TRUE:
      case TAG_FALSE:
        return;
      case TAG_INT32:
        this.readVarint32();
        return;
      case TAG_DOUBLE:
        this.skip(8, "double");
        return;
      case TAG_ONE_BYTE_STRING: {
        const length = this.readVarint32();
        if (length > this.data.length - this.pos) {
          this.fail(
            "STRING_OUT_OF_BOUNDS",
            `string declares ${length} byte(s) but only ${this.data.length - this.pos} remain`,
          );
        }
        this.pos += length;
        this.objectCount += 1;
        return;
      }
      case TAG_BEGIN_OBJECT:
        this.readObject(depth);
        return;
      case TAG_BEGIN_DENSE_ARRAY:
        this.readDenseArray(depth);
        return;
      case TAG_OBJECT_REFERENCE: {
        const id = this.readVarint32();
        if (id >= this.objectCount) {
          this.fail(
            "BAD_REFERENCE",
            `reference #${id} does not point to a previously seen object`,
          );
        }
        return;
      }
      default: {
        const forbidden = FORBIDDEN_TAGS.get(tag);
        if (forbidden !== undefined) {
          this.fail("FORBIDDEN_TAG", `forbidden tag: ${forbidden}`);
        }
        this.fail(
          "UNKNOWN_TAG",
          `unknown tag 0x${tag.toString(16).padStart(2, "0")}`,
        );
      }
    }
  }

  private readObject(depth: number): void {
    this.checkDepth(depth);
    this.enterContainer();
    let pairs = 0;
    while (this.peekByte() !== TAG_END_OBJECT) {
      this.readValue(depth + 1); // key
      this.readValue(depth + 1); // value
      pairs += 1;
      this.addProperties(1);
    }
    this.pos += 1; // consume {
    const declared = this.readVarint32();
    if (declared !== pairs) {
      this.fail(
        "COUNT_MISMATCH",
        `object declares ${declared} propertie(s) but contains ${pairs}`,
      );
    }
  }

  private readDenseArray(depth: number): void {
    this.checkDepth(depth);
    this.enterContainer();
    const length = this.readVarint32();
    for (let i = 0; i < length; i += 1) {
      this.readValue(depth + 1);
      this.addProperties(1);
    }
    let pairs = 0;
    while (this.peekByte() !== TAG_END_DENSE_ARRAY) {
      this.readValue(depth + 1); // key
      this.readValue(depth + 1); // value
      pairs += 1;
      this.addProperties(1);
    }
    this.pos += 1; // consume $
    const declaredProperties = this.readVarint32();
    const declaredLength = this.readVarint32();
    if (declaredProperties !== pairs || declaredLength !== length) {
      this.fail(
        "COUNT_MISMATCH",
        `array trailer declares ${declaredProperties} propertie(s) and length ${declaredLength}, ` +
          `but contains ${pairs} propertie(s) and length ${length}`,
      );
    }
  }

  private checkDepth(depth: number): void {
    if (depth >= this.maxDepth) {
      this.fail("DEPTH_EXCEEDED", `nesting exceeds ${this.maxDepth} levels`);
    }
  }

  private skip(count: number, what: string): void {
    if (count > this.data.length - this.pos) {
      this.fail("UNEXPECTED_EOF", `truncated ${what}`);
    }
    this.pos += count;
  }
}

/** 扫描载荷，返回结构化结果；不抛异常。 */
export function validateV8Payload(
  data: Uint8Array,
  options: PreflightOptions = {},
): PreflightResult {
  try {
    new Reader(data, options).scan();
    return { ok: true };
  } catch (error) {
    if (error instanceof PreflightError) {
      return {
        ok: false,
        code: error.code,
        message: error.message,
        offset: error.offset,
      };
    }
    throw error;
  }
}

/** 扫描载荷，不合法时抛出 PreflightError。 */
export function assertV8Payload(
  data: Uint8Array,
  options: PreflightOptions = {},
): void {
  new Reader(data, options).scan();
}
