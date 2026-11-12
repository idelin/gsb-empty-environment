import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  checkAllResolvedHosts,
  checkResolvedHost,
  parseLockedPackages,
  realNameFromPath,
  resolveRealName,
} from '../src/lockfile.ts';
import type { PackageLock } from '../src/types.ts';

test('路径中最后一个 node_modules 段即包名，支持嵌套与 scoped 包', () => {
  assert.equal(realNameFromPath('node_modules/foo'), 'foo');
  assert.equal(
    realNameFromPath('node_modules/foo/node_modules/bar'),
    'bar',
  );
  assert.equal(
    realNameFromPath('node_modules/@scope/pkg'),
    '@scope/pkg',
  );
  assert.equal(
    realNameFromPath('node_modules/a/node_modules/@scope/pkg'),
    '@scope/pkg',
  );
  assert.equal(realNameFromPath(''), undefined);
});

test('别名条目：name 字段优先作为真实名，且标记 aliased', () => {
  const lock: PackageLock = {
    lockfileVersion: 3,
    packages: {
      'node_modules/left-pad': {
        name: 'right-pad',
        version: '2.1.0',
        resolved: 'https://registry.npmjs.org/right-pad/-/right-pad-2.1.0.tgz',
      },
      'node_modules/normal-pkg': {
        version: '1.0.0',
        resolved: 'https://registry.npmjs.org/normal-pkg/-/normal-pkg-1.0.0.tgz',
      },
      '': { name: 'root-app', version: '0.0.1' },
      'node_modules/linked-pkg': {
        version: '3.0.0',
        link: true,
        resolved: 'node_modules/linked-pkg',
      },
    },
  };

  const packages = parseLockedPackages(lock);
  assert.equal(packages.length, 2);

  const aliased = packages.find((item) => item.installPath === 'node_modules/left-pad')!;
  assert.equal(aliased.realName, 'right-pad');
  assert.equal(aliased.declaredName, 'right-pad');
  assert.equal(aliased.aliased, true);

  const normal = packages.find((item) => item.installPath === 'node_modules/normal-pkg')!;
  assert.equal(normal.realName, 'normal-pkg');
  assert.equal(normal.aliased, false);
});

test('无 name 字段时回退到路径末段', () => {
  const entry = { version: '1.0.0' };
  assert.equal(resolveRealName('node_modules/a/node_modules/b', entry), 'b');
});

test('resolved 主机在允许表内通过，非官方主机报告 disallowed-host', () => {
  const lock: PackageLock = {
    lockfileVersion: 3,
    packages: {
      'node_modules/official': {
        version: '1.0.0',
        resolved: 'https://registry.npmjs.org/official/-/official-1.0.0.tgz',
      },
      'node_modules/evil': {
        version: '2.0.0',
        resolved: 'https://evil-mirror.example/evil/-/evil-2.0.0.tgz',
      },
    },
  };
  const packages = parseLockedPackages(lock);
  const violations = checkAllResolvedHosts(packages);
  assert.equal(violations.length, 1);
  assert.equal(violations[0].realName, 'evil');
  assert.equal(violations[0].reason, 'disallowed-host');
  assert.equal(violations[0].host, 'evil-mirror.example');
});

test('自定义允许主机（企业镜像）可通过', () => {
  const pkg = {
    realName: 'corp-pkg',
    version: '1.0.0',
    installPath: 'node_modules/corp-pkg',
    resolved: 'https://npm.corp.internal/corp-pkg/-/corp-pkg-1.0.0.tgz',
    aliased: false,
  };
  assert.equal(
    checkResolvedHost(pkg, ['npm.corp.internal']),
    undefined,
  );
});

test('resolved 缺失或无法解析分别报告', () => {
  const missing = {
    realName: 'no-resolved',
    version: '1.0.0',
    installPath: 'node_modules/no-resolved',
    aliased: false,
  };
  const missingViolation = checkResolvedHost(missing, ['registry.npmjs.org']);
  assert.equal(missingViolation?.reason, 'missing-resolved');

  const broken = { ...missing, resolved: 'not-a-url' };
  const brokenViolation = checkResolvedHost(broken, ['registry.npmjs.org']);
  assert.equal(brokenViolation?.reason, 'unparsable-resolved');
});

test('主机比较忽略大小写', () => {
  const pkg = {
    realName: 'upper',
    version: '1.0.0',
    installPath: 'node_modules/upper',
    resolved: 'https://REGISTRY.NPMJS.ORG/upper/-/upper-1.0.0.tgz',
    aliased: false,
  };
  assert.equal(checkResolvedHost(pkg, ['registry.npmjs.org']), undefined);
});
