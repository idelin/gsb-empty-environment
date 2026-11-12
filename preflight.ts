/**
 * V8 序列化载荷预检（version 15）。
 *
 * 只扫描字节、绝不还原对象，供消息入口在 v8.deserialize 之前调用。
 * 白名单标签：_ 0 T F I N " o A ^；其余（宿主对象 \、缓冲区、Map、
 * Set、RegExp、BigInt 及未知标签）一律拒绝。
 */

export type PreflightResult = { ok: true } | { ok: false; reason: string };

export const V8_SERIALIZE_VERSION = 15;
export const MAX_DEPTH = 32;
export const MAX_ITEMS = 4096;

const TAG = {
  undefined: 0x5f, // '_'
  null: 0x30, // '0'
  true: 0x54, // 'T'
  false: 0x46, // 'F'
  int32: 0x49, // 'I'
  double: 0x4e, // 'N'
  oneByteString: 0x22, // '"'
  beginObject: 0x6f, // 'o'
  endObject: 0x7b, // '{'
  beginDenseArray: 0x41, // 'A'
  endDenseArray: 0x24, // '$'
  objectReference: 0x5e, // '^'
} as const;

class Reject extends Error {}

class Cursor {
  pos = 0;
  private readonly buf: Uint8Array;

  constructor(buf: Uint8Array) {
    this.buf = buf;
  }

  get remaining(): number {
    return this.buf.length - this.pos;
  }

  byte(): number {
    if (this.pos >= this.buf.length) throw new Reject("载荷在标签处被截断");
    return this.buf[this.pos++];
  }

  peek(): number {
    if (this.pos >= this.buf.length) throw new Reject("载荷被截断");
    return this.buf[this.pos];
  }

  skip(n: number, what: string): void {
    if (n > this.remaining) throw new Reject(`${what}长度超出剩余字节`);
    this.pos += n;
  }

  /** V8 变长整数：每字节 7 位，最高位为续位标志，小端分组。 */
  varint(what: string): number {
    let value = 0;
    let shift = 0;
    for (let i = 0; i < 10; i++) {
      const b = this.byte();
      if (shift >= 32 && (b & 0x7f) !== 0) {
        throw new Reject(`${what}的变长整数过大`);
      }
      value += (b & 0x7f) * 2 ** shift;
      if ((b & 0x80) === 0) return value;
      shift += 7;
    }
    throw new Reject(`${what}的变长整数过长`);
  }
}

interface State {
  /** 可引用对象（字符串 / 对象 / 数组）的已出现数量，即下一个引用编号。 */
  objectCount: number;
  /** 属性与元素总数。 */
  itemCount: number;
}

function addItems(state: State, n: number): void {
  state.itemCount += n;
  if (state.itemCount > MAX_ITEMS) {
    throw new Reject(`属性与元素总数超过 ${MAX_ITEMS}`);
  }
}

function readValue(cursor: Cursor, state: State, depth: number): void {
  const tag = cursor.byte();
  switch (tag) {
    case TAG.undefined:
    case TAG.null:
    case TAG.true:
    case TAG.false:
      return;

    case TAG.int32:
      cursor.varint("int32"); // zigzag 编码，预检只需消费字节
      return;

    case TAG.double:
      cursor.skip(8, "double");
      return;

    case TAG.oneByteString: {
      const length = cursor.varint("字符串长度");
      cursor.skip(length, "字符串");
      state.objectCount++;
      return;
    }

    case TAG.objectReference: {
      const id = cursor.varint("引用编号");
      if (id >= state.objectCount) {
        throw new Reject(`引用编号 ${id} 指向尚未出现的对象`);
      }
      return;
    }

    case TAG.beginObject: {
      if (depth > MAX_DEPTH) throw new Reject(`嵌套超过 ${MAX_DEPTH} 层`);
      state.objectCount++;
      let pairs = 0;
      while (cursor.peek() !== TAG.endObject) {
        readValue(cursor, state, depth + 1); // 键
        readValue(cursor, state, depth + 1); // 值
        pairs++;
        addItems(state, 1);
      }
      cursor.byte(); // '{'
      const declared = cursor.varint("对象属性数");
      if (declared !== pairs) {
        throw new Reject(`对象属性数不符：声明 ${declared}，实际 ${pairs}`);
      }
      return;
    }

    case TAG.beginDenseArray: {
      if (depth > MAX_DEPTH) throw new Reject(`嵌套超过 ${MAX_DEPTH} 层`);
      state.objectCount++;
      const length = cursor.varint("数组长度");
      for (let i = 0; i < length; i++) {
        readValue(cursor, state, depth + 1);
      }
      addItems(state, length);
      let pairs = 0;
      while (cursor.peek() !== TAG.endDenseArray) {
        readValue(cursor, state, depth + 1); // 键
        readValue(cursor, state, depth + 1); // 值
        pairs++;
        addItems(state, 1);
      }
      cursor.byte(); // '$'
      const declaredProps = cursor.varint("数组属性数");
      const declaredLength = cursor.varint("数组长度");
      if (declaredProps !== pairs) {
        throw new Reject(`数组属性数不符：声明 ${declaredProps}，实际 ${pairs}`);
      }
      if (declaredLength !== length) {
        throw new Reject(`数组长度不符：声明 ${declaredLength}，实际 ${length}`);
      }
      return;
    }

    default:
      throw new Reject(
        `拒绝标签 0x${tag.toString(16).padStart(2, "0")}（不在白名单内）`,
      );
  }
}

/**
 * 预检不可信载荷。返回 { ok: true } 才可交给 v8.deserialize。
 */
export function preflightV8Payload(payload: Uint8Array): PreflightResult {
  try {
    if (payload.length < 2) {
      return { ok: false, reason: "载荷过短，缺少头部" };
    }
    if (payload[0] !== 0xff || payload[1] !== V8_SERIALIZE_VERSION) {
      return {
        ok: false,
        reason: `头部不符：须为 0xFF 加版本 ${V8_SERIALIZE_VERSION}`,
      };
    }
    const cursor = new Cursor(payload);
    cursor.pos = 2;
    const state: State = { objectCount: 0, itemCount: 0 };
    readValue(cursor, state, 1);
    if (cursor.remaining !== 0) {
      return { ok: false, reason: "顶层值之后存在多余字节" };
    }
    return { ok: true };
  } catch (err) {
    if (err instanceof Reject) return { ok: false, reason: err.message };
    throw err;
  }
}
