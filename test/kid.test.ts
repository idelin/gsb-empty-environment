import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bytesToKid, kidEquals, kidToBytes, normalizeKid, normalizeKidList } from '../src/kid.ts';

test('normalizeKid 忽略连字符、大小写、URN 前缀', () => {
  const expected = '0123456789abcdef0123456789abcdef';
  assert.equal(normalizeKid('01234567-89AB-CDEF-0123-456789ABCDEF'), expected);
  assert.equal(normalizeKid('urn:uuid:01234567-89ab-cdef-0123-456789abcdef'), expected);
  assert.equal(normalizeKid('0123456789ABCDEF0123456789ABCDEF'), expected);
  assert.equal(normalizeKid('  {01234567-89ab-cdef-0123-456789abcdef}  '), expected);
});

test('normalizeKid 拒绝非法长度或字符', () => {
  assert.equal(normalizeKid('not-a-uuid'), null);
  assert.equal(normalizeKid('01234567-89ab-cdef-0123'), null);
  assert.equal(normalizeKid('zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz'), null);
});

test('normalizeKidList 处理多 KID 与空白', () => {
  const list = normalizeKidList(
    'urn:uuid:01234567-89ab-cdef-0123-456789abcdef  11111111-2222-3333-4444-555555555555',
  );
  assert.deepEqual(list, [
    '0123456789abcdef0123456789abcdef',
    '11111111222233334444555555555555',
  ]);
});

test('bytesToKid / kidToBytes 往返', () => {
  const bytes = kidToBytes('0123456789abcdef0123456789abcdef');
  assert.ok(bytes instanceof Uint8Array);
  assert.equal(bytes.length, 16);
  assert.equal(bytesToKid(bytes), '0123456789abcdef0123456789abcdef');
  assert.equal(bytesToKid(new Uint8Array(8)), null);
});

test('kidEquals 大小写与连字符不敏感', () => {
  assert.ok(kidEquals(
    '01234567-89AB-CDEF-0123-456789ABCDEF',
    '0123456789abcdef0123456789abcdef',
  ));
  assert.equal(kidEquals('11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222'), false);
});
