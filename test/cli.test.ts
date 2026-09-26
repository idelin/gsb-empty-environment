import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const root = fileURLToPath(new URL('..', import.meta.url));

function runCli(): string {
  return execFileSync(
    process.execPath,
    [
      join(root, 'src/cli.ts'),
      join(root, 'data/schemas/user.v1.json'),
      join(root, 'data/schemas/user.v2.json'),
      join(root, 'data/samples'),
    ],
    { encoding: 'utf8' },
  );
}

test('CLI 端到端：五类分组与双路径', () => {
  const output = runCli();

  assert.match(output, /新增失败（旧通过、新失败）】 2 个/);
  assert.match(output, /已修复】 1 个/);
  assert.match(output, /原因变化（两边都失败、规则不同）】 1 个/);
  assert.match(output, /仍然失败（规则相同）】 2 个/);
  assert.match(output, /通过】 1 个/);

  assert.match(output, /002-new-failure-email\.json/);
  assert.match(output, /003-fixed-role\.json/);
  assert.match(output, /004-reason-changed\.json/);
  assert.match(output, /007-new-failure-tags\.json/);

  assert.match(output, /instancePath=\/email schemaPath=\/properties\/email\/pattern/);
  assert.match(output, /instancePath=\/tags\/1 schemaPath=\/properties\/tags\/uniqueItems/);
  assert.match(output, /sha256\(schema\)/);
  assert.match(output, /缓存命中 14 次，未命中（实际计算）14 次/);
});
