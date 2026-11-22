import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ScryptHasher } from '../src/hasher.ts';

test('ScryptHasher 可哈希并校验，且对错误密码返回 false', async () => {
  // 使用极小参数，保持测试快速。
  const hasher = new ScryptHasher(16, 8, 1, 16);
  const stored = await hasher.hash('s3cret');

  assert.notEqual(stored, 's3cret');
  assert.equal(await hasher.verify('s3cret', stored), true);
  assert.equal(await hasher.verify('wrong', stored), false);
  assert.equal(await hasher.verify('s3cret', 'malformed-stored-value'), false);

  // 同一密码两次哈希因随机盐而不同，但都能校验通过。
  const second = await hasher.hash('s3cret');
  assert.notEqual(stored, second);
  assert.equal(await hasher.verify('s3cret', second), true);
});
