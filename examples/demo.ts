/**
 * Runnable tour of the incremental UTF-8 decoder.
 * Run with: npm run demo
 */

import {
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  type Checkpoint,
} from '../src/index.ts';

const encoder = new TextEncoder();
const show = (label: string, value: unknown) =>
  console.log(`${label}\n  ${typeof value === 'string' ? value : JSON.stringify(value, null, 2).replaceAll('\n', '\n  ')}`);

// 1. Chunked decoding: multibyte text with emoji, LF and CRLF, emoji split
//    across four 1-byte chunks.
console.log('=== 1. Incremental decode (chunk boundaries are irrelevant) ===');
const text = '你好，世界 🌍\r\n第二行：café\n';
const bytes = encoder.encode(text);
const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' });
const cuts = [3, 1, 7, 4, 1, 1, 1, 1, 2, 5]; // irregular, splits every char
let offset = 0;
for (const size of cuts) {
  decoder.push(bytes.subarray(offset, offset + size));
  offset += size;
}
decoder.push(bytes.subarray(offset));
const result = decoder.finish();
show('decoded text:', JSON.stringify(result.text));
show('lengths:', { bytes: result.byteLength, utf16: result.utf16Length });

// 2. Bidirectional range queries (UTF-16 offsets, surrogate-safe).
console.log('\n=== 2. Byte <-> UTF-16 range queries ===');
const emojiByte = text.indexOf('🌍'); // utf16 offset of the emoji
const emojiByteStart = encoder.encode(text.slice(0, emojiByte)).length;
show('emoji byte range -> text range:',
  decoder.map.byteRangeToTextRange(emojiByteStart, emojiByteStart + 4));
show('point inside surrogate pair snaps out (utf16):',
  decoder.map.textRangeToByteRange(emojiByte + 1, emojiByte + 1));
const crlfByte = encoder.encode('你好，世界 🌍').length;
show('CRLF byte range -> text range:',
  decoder.map.byteRangeToTextRange(crlfByte, crlfByte + 2));

// 3. Strict mode: absolute byte position of the error.
console.log('\n=== 3. Strict strategy: absolute error positions ===');
const strict = new IncrementalUtf8Decoder({ strategy: 'strict' });
strict.push(encoder.encode('prefix-'));
try {
  strict.push(Uint8Array.of(0xe2, 0x82));
  strict.push(Uint8Array.of(0x28)); // '(' is not a continuation byte
} catch (error) {
  const e = error as Utf8DecodeError;
  show('caught:', `${e.name}: ${e.message} (kind=${e.kind}, detectedAt=${e.detectedAt})`);
}
const truncated = new IncrementalUtf8Decoder({ strategy: 'strict' });
truncated.push(Uint8Array.of(0x66, 0x6f, 0x6f, 0xf0, 0x9f)); // "foo" + half emoji
try {
  truncated.finish();
} catch (error) {
  const e = error as Utf8DecodeError;
  show('finish() caught:', `${e.name}: ${e.message} (kind=${e.kind})`);
}

// 4. Replace strategy: many-to-one collapse is reported explicitly.
console.log('\n=== 4. Replace strategy: explicit collapse intervals ===');
const repl = new IncrementalUtf8Decoder({ strategy: 'replace' });
repl.push(Uint8Array.from([0x41, 0xe2, 0x82, 0x42, 0xf0, 0x9f]));
const replaced = repl.finish();
show('decoded text:', JSON.stringify(replaced.text));
// [0xe2,0x82] -> one U+FFFD (2 bytes -> 1 char), [0xf0,0x9f] -> one U+FFFD.
show('byte range 1..6 -> text range:', repl.map.byteRangeToTextRange(1, 6));

// 5. Serializable checkpoint + digest-bound resume.
console.log('\n=== 5. Checkpoint / resume (bound to stream digest) ===');
const stream1 = encoder.encode('first-half|');
const stream2 = encoder.encode('second-half');
const live = new IncrementalUtf8Decoder({ strategy: 'strict' });
live.push(stream1.subarray(0, 8));
const checkpoint: Checkpoint = JSON.parse(JSON.stringify(live.checkpoint()));
show('checkpoint (trimmed):', {
  version: checkpoint.version,
  consumed: checkpoint.consumed,
  utf16Pos: checkpoint.utf16Pos,
  digest: `${checkpoint.digest.slice(0, 16)}…`,
});
const resumed = IncrementalUtf8Decoder.resume(checkpoint, stream1.subarray(0, 8));
resumed.push(stream1.subarray(8));
resumed.push(stream2);
show('resumed decode:', JSON.stringify(resumed.finish().text));
try {
  IncrementalUtf8Decoder.resume(checkpoint, encoder.encode('evil-str'));
} catch (error) {
  show('foreign stream rejected:', (error as Error).message);
}

// 6. Blocked map: random access without rescanning the prefix.
console.log('\n=== 6. Compressed blocks: O(block) random access ===');
const big = new IncrementalUtf8Decoder({ strategy: 'strict' });
const unit = encoder.encode('ab😀\n');
for (let i = 0; i < 20000; i++) big.push(unit);
const bigResult = big.finish();
big.map.byteToUtf16(bigResult.byteLength - 3);
show('blocks / units scanned for tail query:', {
  blocks: big.map.blockCount,
  scanned: big.map.lastScanSize,
});
