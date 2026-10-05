/**
 * 树状测试名称的规范编码（canonical encoding）。
 *
 * 设计目标：任意合法名称序列（路径）与字节串之间是一一映射，
 * 凡是可能造成"两个不同输入编码结果相同"的名称一律拒绝，而不是静默归一化：
 *
 * - 拒绝空段：空段在拼接式编码中不可见，会产生歧义；
 * - 拒绝段内出现路径分隔符 "/"：防止 "a/b" 与 ["a", "b"] 撞车；
 * - 拒绝 NUL 字符：防止以 NUL 为分隔符的下游编码产生歧义；
 * - 拒绝非 NFC 规范的名称：e-acute 的预组合形式与组合序列显示相同
 *   但码点不同，若静默归一化会让两个不同输入共享同一条流，故直接拒绝；
 * - 拒绝孤立代理项（lone surrogate）：UTF-8 编码会将其替换为 U+FFFD，
 *   导致不同名称编码相同；
 * - 拒绝超长段：长度以 4 字节大端前缀表示，这里限制段长 <= 4096 字节，
 *   尽早暴露笔误。
 *
 * 帧格式（frame）：每段编码为 `4 字节大端长度 || UTF-8 字节`，
 * 多段顺序拼接。长度前缀保证 ["ab","c"] 与 ["a","bc"] 编码不同。
 */

export const MAX_SEGMENT_BYTES = 4096;

const textEncoder = new TextEncoder();

/** 名称不合法（存在编码歧义）时抛出的错误。 */
export class NameEncodingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "NameEncodingError";
  }
}

function hasLoneSurrogate(name: string): boolean {
  for (let i = 0; i < name.length; i++) {
    const code = name.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = name.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true;
      i++;
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      return true;
    }
  }
  return false;
}

/**
 * 校验并将单个名称段编码为 UTF-8 字节。
 * 名称不合法时抛出 {@link NameEncodingError}。
 */
export function encodeSegment(name: string): Uint8Array {
  if (typeof name !== "string") {
    throw new NameEncodingError(`名称段必须是字符串，收到 ${typeof name}`);
  }
  if (name.length === 0) {
    throw new NameEncodingError("名称段不能为空");
  }
  if (name.includes("/")) {
    throw new NameEncodingError(`名称段不能包含路径分隔符 "/"：${JSON.stringify(name)}`);
  }
  if (name.includes(String.fromCharCode(0))) {
    throw new NameEncodingError(`名称段不能包含 NUL 字符：${JSON.stringify(name)}`);
  }
  if (name !== name.normalize("NFC")) {
    throw new NameEncodingError(`名称段不是 NFC 规范形式：${JSON.stringify(name)}`);
  }
  if (hasLoneSurrogate(name)) {
    throw new NameEncodingError(`名称段包含孤立代理项：${JSON.stringify(name)}`);
  }
  const bytes = textEncoder.encode(name);
  if (bytes.length > MAX_SEGMENT_BYTES) {
    throw new NameEncodingError(`名称段超过 ${MAX_SEGMENT_BYTES} 字节上限（实际 ${bytes.length}）`);
  }
  return bytes;
}

/**
 * 将若干字节串按长度前缀帧格式拼接：
 * 每段为 `4 字节大端长度 || 内容`。该编码是单射，拼接不会产生歧义。
 */
export function frame(parts: readonly Uint8Array[]): Uint8Array {
  let total = 0;
  for (const part of parts) {
    if (part.length > 0xffffffff) {
      throw new RangeError("帧字段长度超过 2^32-1 字节");
    }
    total += 4 + part.length;
  }
  const out = new Uint8Array(total);
  const view = new DataView(out.buffer);
  let offset = 0;
  for (const part of parts) {
    view.setUint32(offset, part.length);
    out.set(part, offset + 4);
    offset += 4 + part.length;
  }
  return out;
}

/** 将常量标签（域分隔符）编码为 UTF-8 字节。 */
export function encodeTag(tag: string): Uint8Array {
  return textEncoder.encode(tag);
}
