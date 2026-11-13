import test from 'node:test';
import assert from 'node:assert/strict';
import {
  LargeBlobError,
  MAX_BLOB_SIZE,
  readLargeBlob,
} from '../index.ts';

const encoder = new TextEncoder();

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.byteLength, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.byteLength;
  }
  return out;
}

async function gzip(data: Uint8Array): Promise<Uint8Array> {
  const stream = new Response(data)
    .body!.pipeThrough(new CompressionStream('gzip'));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

function aad(origSize: bigint): Uint8Array {
  const out = new Uint8Array(12);
  out.set([0x62, 0x6c, 0x6f, 0x62]); // "blob"
  new DataView(out.buffer).setBigUint64(4, origSize, true);
  return out;
}

interface EntrySpec {
  plain: Uint8Array;
  /** 写入头部与 AAD 的 origSize，缺省为明文真实长度。 */
  claimedOrigSize?: bigint;
}

/** 按包内格式生成带尾部摘要的序列化数组；tamper 可在追加摘要前改写 body。 */
async function seal(
  specs: EntrySpec[],
  keyBytes: Uint8Array,
  tamper?: (body: Uint8Array) => Uint8Array,
): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey(
    'raw',
    keyBytes,
    { name: 'AES-GCM' },
    false,
    ['encrypt'],
  );
  const count = new Uint8Array(4);
  new DataView(count.buffer).setUint32(0, specs.length, true);
  const parts: Uint8Array[] = [count];
  for (const spec of specs) {
    const origSize = spec.claimedOrigSize ?? BigInt(spec.plain.byteLength);
    const nonce = crypto.getRandomValues(new Uint8Array(12));
    const compressed = await gzip(spec.plain);
    const ciphertext = new Uint8Array(
      await crypto.subtle.encrypt(
        { name: 'AES-GCM', iv: nonce, additionalData: aad(origSize) },
        key,
        compressed,
      ),
    );
    const header = new Uint8Array(12);
    const view = new DataView(header.buffer);
    view.setBigUint64(0, origSize, true);
    view.setUint32(8, ciphertext.byteLength, true);
    parts.push(header, nonce, ciphertext);
  }
  let body = concat(parts);
  if (tamper) body = tamper(body);
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', body));
  return concat([body, digest.subarray(0, 16)]);
}

/** 手工拼一个条目（不加密），用于测试解密前的尺寸拒绝路径。 */
async function sealRawEntry(
  origSize: bigint,
  cipherLen: number,
): Promise<Uint8Array> {
  const count = new Uint8Array(4);
  new DataView(count.buffer).setUint32(0, 1, true);
  const header = new Uint8Array(12);
  const view = new DataView(header.buffer);
  view.setBigUint64(0, origSize, true);
  view.setUint32(8, cipherLen, true);
  const body = concat([count, header, new Uint8Array(12 + cipherLen)]);
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', body));
  return concat([body, digest.subarray(0, 16)]);
}

async function expectCode(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(promise, (error: unknown) => {
    assert.ok(error instanceof LargeBlobError, `期望 LargeBlobError，收到 ${error}`);
    assert.equal(error.code, code);
    return true;
  });
}

test('往返：多条目解密解压成功', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const blobs = [
    encoder.encode('hello largeBlob'),
    new Uint8Array(0),
    crypto.getRandomValues(new Uint8Array(60_000)),
  ];
  const sealed = await seal(blobs.map((plain) => ({ plain })), key);
  const out = await readLargeBlob(sealed, key);
  assert.equal(out.length, blobs.length);
  for (let i = 0; i < blobs.length; i++) assert.deepEqual(out[i], blobs[i]);
});

test('往返：空条目列表', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([], key);
  assert.deepEqual(await readLargeBlob(sealed, key), []);
});

test('往返：接受 CryptoKey 与 ArrayBuffer 输入', async () => {
  const keyBytes = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('cryptokey') }], keyBytes);
  const cryptoKey = await crypto.subtle.importKey(
    'raw',
    keyBytes,
    { name: 'AES-GCM' },
    false,
    ['decrypt'],
  );
  const buffer = sealed.buffer.slice(0) as ArrayBuffer;
  const out = await readLargeBlob(buffer, cryptoKey);
  assert.deepEqual(out[0], encoder.encode('cryptokey'));
});

test('篡改 body 任意字节 → 摘要不符', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('data') }], key);
  sealed[10] ^= 0xff;
  await expectCode(readLargeBlob(sealed, key), 'ERR_TAG_MISMATCH');
});

test('篡改尾部摘要 → 摘要不符', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('data') }], key);
  sealed[sealed.length - 1] ^= 0x01;
  await expectCode(readLargeBlob(sealed, key), 'ERR_TAG_MISMATCH');
});

test('数组过短 → 格式错误', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  await expectCode(readLargeBlob(new Uint8Array(10), key), 'ERR_MALFORMED');
});

test('条目中途截断（摘要有效）→ 格式错误', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal(
    [{ plain: encoder.encode('truncate me') }],
    key,
    (body) => body.subarray(0, body.length - 5),
  );
  await expectCode(readLargeBlob(sealed, key), 'ERR_MALFORMED');
});

test('条目后有多余字节（摘要有效）→ 格式错误', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('x') }], key, (body) =>
    concat([body, new Uint8Array(3)]),
  );
  await expectCode(readLargeBlob(sealed, key), 'ERR_MALFORMED');
});

test('origSize 超过 1 MiB → 解密前拒绝', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await sealRawEntry(BigInt(MAX_BLOB_SIZE) + 1n, 16);
  await expectCode(readLargeBlob(sealed, key), 'ERR_SIZE_LIMIT');
});

test('origSize 为 u64 最大值 → 解密前拒绝', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await sealRawEntry(0xffffffffffffffffn, 16);
  await expectCode(readLargeBlob(sealed, key), 'ERR_SIZE_LIMIT');
});

test('自定义 maxSize 生效', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: new Uint8Array(100) }], key);
  await expectCode(
    readLargeBlob(sealed, key, { maxSize: 50 }),
    'ERR_SIZE_LIMIT',
  );
  assert.equal((await readLargeBlob(sealed, key, { maxSize: 100 })).length, 1);
});

test('解压炸弹：声明 1 MiB 实际解压 2 MiB → 流式中止', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const bomb = new Uint8Array(2 * 1024 * 1024); // 压缩后仅约 2 KiB
  const sealed = await seal(
    [{ plain: bomb, claimedOrigSize: BigInt(MAX_BLOB_SIZE) }],
    key,
  );
  await expectCode(readLargeBlob(sealed, key), 'ERR_SIZE_MISMATCH');
});

test('声明的 origSize 大于实际解压长度 → 长度不符', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal(
    [{ plain: new Uint8Array(50), claimedOrigSize: 100n }],
    key,
  );
  await expectCode(readLargeBlob(sealed, key), 'ERR_SIZE_MISMATCH');
});

test('篡改密文（摘要有效）→ GCM 解密失败', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('secret') }], key, (body) => {
    const tampered = body.slice();
    tampered[4 + 12 + 12 + 2] ^= 0xff; // 密文第 3 字节
    return tampered;
  });
  await expectCode(readLargeBlob(sealed, key), 'ERR_DECRYPT');
});

test('错误的密钥 → GCM 解密失败', async () => {
  const keyA = crypto.getRandomValues(new Uint8Array(32));
  const keyB = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('secret') }], keyA);
  await expectCode(readLargeBlob(sealed, keyB), 'ERR_DECRYPT');
});

test('密钥长度不是 32 字节 → 密钥错误', async () => {
  const key = crypto.getRandomValues(new Uint8Array(32));
  const sealed = await seal([{ plain: encoder.encode('x') }], key);
  await expectCode(
    readLargeBlob(sealed, new Uint8Array(16)),
    'ERR_KEY',
  );
});
