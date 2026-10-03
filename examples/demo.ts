import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { decodeMessage, encodeMessage, ProtobufDecodeError } from '../src/index.ts';
import { addressBookSchema, sampleBook } from '../test/helpers/fixture.ts';

const book = sampleBook();

// 1. 编码为紧凑二进制
const encoded = encodeMessage(addressBookSchema, book);
console.log(`encoded ${encoded.length} bytes:`);
console.log(`  ${Buffer.from(encoded).toString('hex')}`);

// 2. 持久化到磁盘
const dir = mkdtempSync(join(tmpdir(), 'pbcodec-demo-'));
const file = join(dir, 'addressbook.pb');
writeFileSync(file, encoded);
console.log(`persisted to ${file}`);

// 3. 读回并零拷贝解析(直接映射文件缓冲,不做中间拷贝)
const fileBuffer = readFileSync(file);
const mapped = new Uint8Array(fileBuffer.buffer, fileBuffer.byteOffset, fileBuffer.byteLength);
const decoded = decodeMessage(addressBookSchema, mapped);
console.log('decoded:', JSON.stringify(decoded, (_k, v) => (typeof v === 'bigint' ? v.toString() : v), 2));

const avatar = (decoded.people as Array<Record<string, unknown>>)[0].avatar as Uint8Array;
console.log(`zero-copy check: avatar aliases file buffer -> ${avatar.buffer === fileBuffer.buffer}`);

// 4. 截断防护演示
const truncated = encoded.subarray(0, encoded.length - 3);
try {
  decodeMessage(addressBookSchema, truncated);
  console.log('truncated buffer decoded (cut landed on a field boundary)');
} catch (e) {
  console.log(`truncation safely rejected: ${(e as ProtobufDecodeError).message}`);
}
