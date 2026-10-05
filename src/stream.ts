/**
 * 按树状测试名称派生的确定性随机流。
 *
 * 结构：
 * - 每条流持有一个 256 位密钥，由父流密钥与名称段的规范编码派生：
 *   `childKey = SHA-256(frame([TAG_CHILD, parentKey, segment]))`
 *   根流由种子派生：`rootKey = SHA-256(frame([TAG_ROOT, seed]))`
 * - 输出采用计数器模式：第 i 个输出块为
 *   `SHA-256(frame([TAG_DRAW, key, counter_i]))`，计数器 128 位大端。
 *   输出是 (密钥, 位置) 的纯函数，因此分批生成与一次生成必然等价。
 *
 * 性质：
 * - 派生子流是纯函数，不触碰父流与兄弟流的任何状态，
 *   新增兄弟测试不会改变已有测试的数据；
 * - 流完全由路径（种子 + 名称序列）决定，重命名测试即得到全新流；
 * - 全程只使用整数运算与纯 TS 版 SHA-256，不使用宿主默认随机源
 *   （Math.random / crypto.getRandomValues 等），跨平台逐位一致。
 */

import { sha256 } from "./sha256.ts";
import { encodeSegment, encodeTag, frame } from "./encoding.ts";

const TAG_ROOT = encodeTag("gsb-random-stream/v1/root");
const TAG_CHILD = encodeTag("gsb-random-stream/v1/child");
const TAG_DRAW = encodeTag("gsb-random-stream/v1/draw");

const BLOCK_BYTES = 32;
const COUNTER_BYTES = 16;
const TWO_POW_53 = 9007199254740992; // 2^53
const TWO_POW_64 = 1n << 64n;

function seedToBytes(seed: string | Uint8Array): Uint8Array {
  if (typeof seed === "string") {
    if (seed.length === 0) {
      throw new RangeError("种子字符串不能为空");
    }
    return encodeSegment(seed);
  }
  if (seed instanceof Uint8Array) {
    if (seed.length === 0) {
      throw new RangeError("种子字节串不能为空");
    }
    return seed;
  }
  throw new TypeError("种子必须是 string 或 Uint8Array");
}

export class RandomStream {
  readonly #key: Uint8Array;
  readonly #path: readonly string[];
  #counter: bigint;
  #block: Uint8Array;
  #offset: number;

  private constructor(key: Uint8Array, path: readonly string[]) {
    this.#key = key;
    this.#path = path;
    this.#counter = 0n;
    this.#block = new Uint8Array(0);
    this.#offset = 0;
  }

  /** 由种子创建根流。相同种子永远得到相同的流。 */
  static fromSeed(seed: string | Uint8Array): RandomStream {
    const key = sha256(frame([TAG_ROOT, seedToBytes(seed)]));
    return new RandomStream(key, []);
  }

  /**
   * 派生指定名称的子流。纯函数：不修改当前流，
   * 相同名称永远派生出相同的子流，不同名称得到独立子流。
   * 名称存在编码歧义时抛出 NameEncodingError。
   */
  child(name: string): RandomStream {
    const segment = encodeSegment(name);
    const key = sha256(frame([TAG_CHILD, this.#key, segment]));
    return new RandomStream(key, [...this.#path, name]);
  }

  /** 从根到当前流的名称路径（根流为空数组）。 */
  get path(): readonly string[] {
    return this.#path;
  }

  /** 当前流密钥的十六进制表示，可用于调试与比对。 */
  get keyHex(): string {
    let hex = "";
    for (const byte of this.#key) {
      hex += byte.toString(16).padStart(2, "0");
    }
    return hex;
  }

  #advanceBlock(): void {
    const counter = new Uint8Array(COUNTER_BYTES);
    let value = this.#counter;
    for (let i = COUNTER_BYTES - 1; i >= 0; i--) {
      counter[i] = Number(value & 0xffn);
      value >>= 8n;
    }
    this.#block = sha256(frame([TAG_DRAW, this.#key, counter]));
    this.#offset = 0;
    this.#counter += 1n;
  }

  /** 取出 n 个字节。 */
  nextBytes(n: number): Uint8Array {
    if (!Number.isSafeInteger(n) || n < 0) {
      throw new RangeError(`字节数必须是非负安全整数，收到 ${n}`);
    }
    const out = new Uint8Array(n);
    let written = 0;
    while (written < n) {
      if (this.#offset >= this.#block.length) {
        this.#advanceBlock();
      }
      const take = Math.min(this.#block.length - this.#offset, n - written);
      out.set(this.#block.subarray(this.#offset, this.#offset + take), written);
      this.#offset += take;
      written += take;
    }
    return out;
  }

  /** 取出一个无符号 32 位整数（大端解读）。 */
  nextUint32(): number {
    const b = this.nextBytes(4);
    return ((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0;
  }

  /** 取出 [0, 1) 区间均匀分布的浮点数（53 位精度，整数运算构造）。 */
  nextFloat(): number {
    const b = this.nextBytes(7);
    const high = ((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0;
    const low = (((b[4] << 16) | (b[5] << 8) | b[6]) >>> 3);
    return (high * 2097152 + low) / TWO_POW_53;
  }

  /**
   * 取出 [min, max) 区间均匀分布的整数（拒绝采样，无模偏倚）。
   * min、max 必须为安全整数且 min < max。
   */
  nextInt(min: number, max: number): number {
    if (!Number.isSafeInteger(min) || !Number.isSafeInteger(max)) {
      throw new RangeError("区间端点必须是安全整数");
    }
    if (min >= max) {
      throw new RangeError(`区间为空：[${min}, ${max})`);
    }
    const range = BigInt(max) - BigInt(min);
    const limit = TWO_POW_64 - (TWO_POW_64 % range);
    for (;;) {
      const b = this.nextBytes(8);
      let x = 0n;
      for (const byte of b) {
        x = (x << 8n) | BigInt(byte);
      }
      if (x < limit) {
        return min + Number(x % range);
      }
    }
  }
}
