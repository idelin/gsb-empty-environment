import { test } from 'node:test';
import assert from 'node:assert/strict';

import { compareSemVer, compareVersions, parseSemVer } from '../src/semver.ts';

test('预发布版本小于对应正式版', () => {
  const prerelease = parseSemVer('1.0.0-alpha')!;
  const release = parseSemVer('1.0.0')!;
  assert.equal(compareSemVer(prerelease, release), -1);
  assert.equal(compareSemVer(release, prerelease), 1);
});

test('预发布标识符按 SemVer 规则逐段比较', () => {
  assert.equal(compareVersions('1.0.0-alpha', '1.0.0-alpha.1'), -1);
  assert.equal(compareVersions('1.0.0-alpha.1', '1.0.0-alpha.beta'), -1);
  assert.equal(compareVersions('1.0.0-alpha.beta', '1.0.0-beta'), -1);
  assert.equal(compareVersions('1.0.0-beta', '1.0.0-beta.2'), -1);
  assert.equal(compareVersions('1.0.0-beta.2', '1.0.0-beta.11'), -1);
  assert.equal(compareVersions('1.0.0-beta.11', '1.0.0-rc.1'), -1);
  assert.equal(compareVersions('1.0.0-rc.1', '1.0.0'), -1);
});

test('数值标识符优先级低于字母数字标识符', () => {
  assert.equal(compareVersions('1.0.0-1', '1.0.0-alpha'), -1);
});

test('构建元数据不参与优先级比较', () => {
  assert.equal(compareVersions('1.0.0+build.42', '1.0.0'), 0);
  assert.equal(compareVersions('1.0.0-alpha+exp', '1.0.0-alpha'), 0);
});

test('核心版本号按数值比较', () => {
  assert.equal(compareVersions('1.2.3', '1.2.4'), -1);
  assert.equal(compareVersions('1.2.3', '1.3.0'), -1);
  assert.equal(compareVersions('2.0.0', '1.99.99'), 1);
  assert.equal(compareVersions('1.2.3', '1.2.3'), 0);
});

test('兼容可选的 v 前缀', () => {
  assert.ok(parseSemVer('v1.2.3'));
  assert.equal(compareVersions('v1.2.3', '1.2.3'), 0);
});

test('非法版本解析为 undefined', () => {
  assert.equal(parseSemVer('not-a-version'), undefined);
  assert.equal(parseSemVer('1.2'), undefined);
  assert.equal(compareVersions('bogus', '1.0.0'), undefined);
});
