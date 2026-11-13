/**
 * largeBlob 读取包（浏览器宿主，仅依赖 WebCrypto 与 DecompressionStream）。
 *
 * 序列化布局（整数均为小端）：
 *
 *   body :=
 *     count          u32le           条目数
 *     count 个条目，每个条目 :=
 *       origSize     u64le           解压后明文长度（字节）
 *       cipherLen    u32le           密文长度（含 16 字节 GCM 认证标签）
 *       nonce        12 字节         AES-GCM IV
 *       ciphertext   cipherLen 字节  gzip(明文) 的 AES-256-GCM 密文
 *   tag  := SHA-256(body) 的前 16 字节，追加在 body 之后
 *
 * 每个条目的 AES-GCM 附加数据（AAD）为：
 *   "blob"（4 字节 ASCII） || origSize（u64le，8 字节）
 *
 * 安全不变量：
 *  - 先校验尾部摘要，再解析与解密任何条目；
 *  - origSize 超过 1 MiB（或调用方给定的更小上限）直接拒绝，不解密、不解压；
 *  - 解压流式计数，输出一旦超过 origSize 立即中止，抵御解压炸弹；
 *  - 解压结果长度必须恰好等于 origSize。
 */

/** 单条明文硬上限：1 MiB。 */
export const MAX_BLOB_SIZE = 1 << 20;

const TAG_LENGTH = 16;
const NONCE_LENGTH = 12;
const GCM_TAG_LENGTH = 16;
const MIN_ENTRY_LENGTH = 8 + 4 + NONCE_LENGTH + GCM_TAG_LENGTH;
const AAD_PREFIX = new Uint8Array([0x62, 0x6c, 0x6f, 0x62]); // "blob"

export type LargeBlobErrorCode =
  | 'ERR_MALFORMED'
  | 'ERR_TAG_MISMATCH'
  | 'ERR_KEY'
  | 'ERR_SIZE_LIMIT'
  | 'ERR_DECRYPT'
  | 'ERR_DECOMPRESS'
  | 'ERR_SIZE_MISMATCH';

export class LargeBlobError extends Error {
  readonly code: LargeBlobErrorCode;
  readonly entryIndex: number | undefined;

  constructor(code: LargeBlobErrorCode, message: string, entryIndex?: number) {
    super(message);
    this.name = 'LargeBlobError';
    this.code = code;
    this.entryIndex = entryIndex;
  }
}

/** AES-256 密钥：32 字节原始密钥，或已导入的 AES-GCM CryptoKey。 */
export type LargeBlobKey = CryptoKey | Uint8Array | ArrayBuffer;

export interface ReadLargeBlobOptions {
  /** 单条明文上限（字节），默认 1 MiB；超过 1 MiB 时按 1 MiB 处理。 */
  maxSize?: number;
}

/**
 * 读取认证器返回的序列化 largeBlob 数组，返回各条目解密解压后的明文。
 * 任何完整性、尺寸或解密校验失败都会抛出 LargeBlobError。
 */
export async function readLargeBlob(
  serialized: Uint8Array | ArrayBuffer,
  key: LargeBlobKey,
  options: ReadLargeBlobOptions = {},
): Promise<Uint8Array[]> {
  let maxSize = options.maxSize ?? MAX_BLOB_SIZE;
  if (!Number.isInteger(maxSize) || maxSize < 0) {
    throw new TypeError(`maxSize 必须是非负整数，收到 ${maxSize}`);
  }
  if (maxSize > MAX_BLOB_SIZE) maxSize = MAX_BLOB_SIZE;

  const bytes =
    serialized instanceof Uint8Array ? serialized : new Uint8Array(serialized);
  if (bytes.byteLength < 4 + TAG_LENGTH) {
    throw new LargeBlobError('ERR_MALFORMED', '数组太短，缺少条目计数或尾部摘要');
  }

  const body = bytes.subarray(0, bytes.byteLength - TAG_LENGTH);
  const tag = bytes.subarray(bytes.byteLength - TAG_LENGTH);
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', body));
  let diff = 0;
  for (let i = 0; i < TAG_LENGTH; i++) diff |= digest[i] ^ tag[i];
  if (diff !== 0) {
    throw new LargeBlobError(
      'ERR_TAG_MISMATCH',
      '尾部 16 字节与前文 SHA-256 摘要的前 16 字节不符',
    );
  }

  const cryptoKey = await importKey(key);

  const reader = new Reader(body);
  const count = reader.u32();
  if (count > reader.remaining / MIN_ENTRY_LENGTH) {
    throw new LargeBlobError('ERR_MALFORMED', '条目数超出数组容量');
  }
  const entries: RawEntry[] = [];
  for (let i = 0; i < count; i++) {
    const origSize = reader.u64();
    const cipherLen = reader.u32();
    if (cipherLen < GCM_TAG_LENGTH) {
      throw new LargeBlobError(
        'ERR_MALFORMED',
        `条目 ${i} 的密文长度 ${cipherLen} 小于 GCM 认证标签`,
        i,
      );
    }
    const nonce = reader.take(NONCE_LENGTH);
    const ciphertext = reader.take(cipherLen);
    entries.push({ origSize, nonce, ciphertext });
  }
  if (reader.remaining !== 0) {
    throw new LargeBlobError('ERR_MALFORMED', '最后一个条目之后存在多余字节');
  }

  const out: Uint8Array[] = [];
  for (let i = 0; i < entries.length; i++) {
    out.push(await openEntry(entries[i], cryptoKey, maxSize, i));
  }
  return out;
}

interface RawEntry {
  origSize: bigint;
  nonce: Uint8Array;
  ciphertext: Uint8Array;
}

async function openEntry(
  entry: RawEntry,
  key: CryptoKey,
  maxSize: number,
  index: number,
): Promise<Uint8Array> {
  const { origSize, nonce, ciphertext } = entry;
  if (origSize > BigInt(maxSize)) {
    throw new LargeBlobError(
      'ERR_SIZE_LIMIT',
      `条目 ${index} 的 origSize ${origSize} 超过上限 ${maxSize}`,
      index,
    );
  }
  const expected = Number(origSize);

  const aad = new Uint8Array(AAD_PREFIX.length + 8);
  aad.set(AAD_PREFIX, 0);
  new DataView(aad.buffer).setBigUint64(AAD_PREFIX.length, origSize, true);

  let compressed: ArrayBuffer;
  try {
    compressed = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: nonce, additionalData: aad, tagLength: 128 },
      key,
      ciphertext,
    );
  } catch {
    throw new LargeBlobError(
      'ERR_DECRYPT',
      `条目 ${index} AES-GCM 解密失败（数据被篡改或密钥不符）`,
      index,
    );
  }

  const plain = await gunzipLimited(new Uint8Array(compressed), expected, index);
  if (plain.byteLength !== expected) {
    throw new LargeBlobError(
      'ERR_SIZE_MISMATCH',
      `条目 ${index} 解压长度 ${plain.byteLength} 与 origSize ${expected} 不符`,
      index,
    );
  }
  return plain;
}

/** 流式解压 gzip，输出超过 limit 立即中止，避免解压炸弹耗尽内存。 */
async function gunzipLimited(
  data: Uint8Array,
  limit: number,
  index: number,
): Promise<Uint8Array> {
  let stream: ReadableStream<Uint8Array>;
  try {
    stream = new Response(data)
      .body!.pipeThrough(new DecompressionStream('gzip'));
  } catch {
    throw new LargeBlobError('ERR_DECOMPRESS', `条目 ${index} 无法创建解压流`, index);
  }
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > limit) {
        void reader.cancel().catch(() => undefined);
        throw new LargeBlobError(
          'ERR_SIZE_MISMATCH',
          `条目 ${index} 解压输出超过声明的 origSize ${limit}，疑似解压炸弹`,
          index,
        );
      }
      chunks.push(value);
    }
  } catch (cause) {
    if (cause instanceof LargeBlobError) throw cause;
    throw new LargeBlobError('ERR_DECOMPRESS', `条目 ${index} gzip 解压失败`, index);
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return out;
}

async function importKey(key: LargeBlobKey): Promise<CryptoKey> {
  if (key instanceof CryptoKey) {
    const algorithm = key.algorithm as { name?: string; length?: number };
    if (
      algorithm.name !== 'AES-GCM' ||
      algorithm.length !== 256 ||
      !key.usages.includes('decrypt')
    ) {
      throw new LargeBlobError(
        'ERR_KEY',
        'CryptoKey 必须是可用于 decrypt 的 AES-256-GCM 密钥',
      );
    }
    return key;
  }
  const raw = key instanceof Uint8Array ? key : new Uint8Array(key);
  if (raw.byteLength !== 32) {
    throw new LargeBlobError(
      'ERR_KEY',
      `AES-256 密钥须为 32 字节，收到 ${raw.byteLength} 字节`,
    );
  }
  return crypto.subtle.importKey('raw', raw, { name: 'AES-GCM' }, false, [
    'decrypt',
  ]);
}

class Reader {
  private readonly bytes: Uint8Array;
  private readonly view: DataView;
  private offset = 0;

  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }

  get remaining(): number {
    return this.bytes.byteLength - this.offset;
  }

  u32(): number {
    this.ensure(4);
    const value = this.view.getUint32(this.offset, true);
    this.offset += 4;
    return value;
  }

  u64(): bigint {
    this.ensure(8);
    const value = this.view.getBigUint64(this.offset, true);
    this.offset += 8;
    return value;
  }

  take(length: number): Uint8Array {
    this.ensure(length);
    const value = this.bytes.subarray(this.offset, this.offset + length);
    this.offset += length;
    return value;
  }

  private ensure(length: number): void {
    if (length > this.remaining) {
      throw new LargeBlobError('ERR_MALFORMED', '数组在条目中途被截断');
    }
  }
}
