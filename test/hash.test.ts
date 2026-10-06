import { test, assert } from './harness';
import { bucketKey, bucketOf, fnv1a32, hashBytes } from '../src/hash';

// Public FNV-1a 32-bit test vectors (Landon Curt Noll's reference suite
// plus cross-checked UTF-8 cases from an independent implementation).
test('hash: public FNV-1a 32-bit vectors', () => {
  assert.strictEqual(fnv1a32(''), 0x811c9dc5);
  assert.strictEqual(fnv1a32('a'), 0xe40c292c);
  assert.strictEqual(fnv1a32('foobar'), 0xbf9cf968);
  assert.strictEqual(fnv1a32('hello'), 0x4f9f2cab);
});

test('hash: Unicode is encoded as UTF-8 bytes before hashing', () => {
  // U+00E9 'é' -> 0xC3 0xA9; U+4E2D U+6587 '中文'; U+1F600 '😀' -> F0 9F 98 80
  assert.deepStrictEqual(Array.from(new TextEncoder().encode('é')), [0xc3, 0xa9]);
  assert.deepStrictEqual(Array.from(new TextEncoder().encode('😀')), [0xf0, 0x9f, 0x98, 0x80]);
  // Values cross-checked against an independent (Python) FNV-1a implementation.
  assert.strictEqual(fnv1a32('é'), 0x1e9de8c1);
  assert.strictEqual(fnv1a32('中文'), 0xc488f045);
  assert.strictEqual(fnv1a32('😀'), 0x33a29608);
  // Hashing the raw bytes directly must agree with hashing the string.
  assert.strictEqual(hashBytes(new Uint8Array([0xc3, 0xa9])), fnv1a32('é'));
  // NFC 'é' (2 bytes) and NFD 'e + combining accent' (3 bytes) differ.
  assert.notStrictEqual(fnv1a32('é'), fnv1a32('é'));
});

test('hash: bucket is hash mod 10000', () => {
  assert.strictEqual(bucketOf(''), 0x811c9dc5 % 10000); // 6261
  assert.strictEqual(bucketOf('中文'), 8741);
  assert.strictEqual(bucketOf('😀'), 3256);
  for (const key of ['x', 'flag:u-1', 'salt:flag:u-1', '中文:😀']) {
    const b = bucketOf(key);
    assert.ok(b >= 0 && b < 10000, `bucket in range for ${key}`);
  }
});

test('hash: salted bucket key format', () => {
  assert.strictEqual(bucketKey('flag-x', 'u-1'), 'flag-x:u-1');
  assert.strictEqual(bucketKey('flag-x', 'u-1', 'saltA'), 'saltA:flag-x:u-1');
  assert.notStrictEqual(bucketOf(bucketKey('f', 'u', 's')), bucketOf(bucketKey('f', 'u')));
});
