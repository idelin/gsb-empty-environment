import { test } from 'node:test';
import assert from 'node:assert/strict';

import { parseLockfileText, scanLockfile } from '../index.ts';
import type { OsvAdvisory, PackageLock, ScanOptions } from '../index.ts';

function advisory(
  id: string,
  packageName: string,
  events: Array<Record<string, string>>,
): OsvAdvisory {
  return {
    id,
    packageName,
    ranges: [{ type: 'SEMVER', events }],
  };
}

function buildLock(): PackageLock {
  return {
    name: 'gate-app',
    version: '1.0.0',
    lockfileVersion: 3,
    packages: {
      '': { name: 'gate-app', version: '1.0.0' },
      'node_modules/alias-display': {
        name: 'real-vuln-lib',
        version: '1.2.0',
        resolved:
          'https://registry.npmjs.org/real-vuln-lib/-/real-vuln-lib-1.2.0.tgz',
      },
      'node_modules/clean-pkg': {
        version: '4.0.0',
        resolved: 'https://registry.npmjs.org/clean-pkg/-/clean-pkg-4.0.0.tgz',
      },
      'node_modules/edge-pkg': {
        version: '2.0.0',
        resolved: 'https://registry.npmjs.org/edge-pkg/-/edge-pkg-2.0.0.tgz',
      },
      'node_modules/last-pkg': {
        version: '3.0.0',
        resolved: 'https://registry.npmjs.org/last-pkg/-/last-pkg-3.0.0.tgz',
      },
      'node_modules/evil-host-pkg': {
        version: '1.0.0',
        resolved: 'https://evil-mirror.example/evil-host-pkg/-/evil-host-pkg-1.0.0.tgz',
      },
      'node_modules/no-data-pkg': {
        version: '0.1.0',
        resolved:
          'https://registry.npmjs.org/no-data-pkg/-/no-data-pkg-0.1.0.tgz',
      },
    },
  };
}

function buildOptions(): ScanOptions {
  return {
    advisories: {
      /** 别名包：必须按 name 字段的真实名命中，而不是路径显示名。 */
      'real-vuln-lib': [
        advisory('OSV-ALIAS-1', 'real-vuln-lib', [
          { introduced: '0' },
          { fixed: '2.0.0' },
        ]),
      ],
      'clean-pkg': [],
      /** fixed 边界：2.0.0 本身不受影响。 */
      'edge-pkg': [
        advisory('OSV-EDGE-1', 'edge-pkg', [
          { introduced: '1.0.0' },
          { fixed: '2.0.0' },
        ]),
      ],
      /** last_affected 边界：3.0.0 本身仍受影响。 */
      'last-pkg': [
        advisory('OSV-LAST-1', 'last-pkg', [
          { introduced: '1.0.0' },
          { last_affected: '3.0.0' },
        ]),
      ],
      'evil-host-pkg': [],
      /** 故意不提供 no-data-pkg 的 OSV 数据。 */
    },
  };
}

test('别名包按真实名称匹配漏洞，resolved 非官方主机被报告', () => {
  const result = scanLockfile(buildLock(), buildOptions());

  const aliased = result.packages.find(
    (item) => item.installPath === 'node_modules/alias-display',
  )!;
  assert.equal(aliased.realName, 'real-vuln-lib');
  assert.equal(aliased.aliased, true);

  const aliasEvaluation = result.evaluations.find(
    (item) => item.realName === 'real-vuln-lib',
  )!;
  assert.deepEqual(aliasEvaluation.affectedBy, ['OSV-ALIAS-1']);
  assert.equal(aliasEvaluation.evaluated, true);

  const hostViolation = result.hostViolations.find(
    (item) => item.realName === 'evil-host-pkg',
  );
  assert.ok(hostViolation);
  assert.equal(hostViolation!.reason, 'disallowed-host');
});

test('introduced 为 0、fixed 与 last_affected 边界判定正确', () => {
  const result = scanLockfile(buildLock(), buildOptions());
  const byName = new Map(
    result.evaluations.map((item) => [item.realName, item]),
  );

  assert.deepEqual(byName.get('real-vuln-lib')!.affectedBy, ['OSV-ALIAS-1']);
  assert.deepEqual(byName.get('edge-pkg')!.affectedBy, []);
  assert.deepEqual(byName.get('last-pkg')!.affectedBy, ['OSV-LAST-1']);
  assert.deepEqual(byName.get('clean-pkg')!.affectedBy, []);
});

test('存在未评估条目时整体不闭合，门禁不通过', () => {
  const result = scanLockfile(buildLock(), buildOptions());

  const missing = result.evaluations.find(
    (item) => item.realName === 'no-data-pkg',
  )!;
  assert.equal(missing.evaluated, false);
  assert.equal(missing.reason, 'no-osv-data');

  assert.equal(result.closed, false);
  assert.equal(result.passed, false);
  assert.ok(
    result.errors.some((message) => message.includes('no-data-pkg')),
  );
});

test('补齐全部评估、移除主机违规且无漏洞后才闭合通过', () => {
  const lock = buildLock();
  delete lock.packages!['node_modules/evil-host-pkg'];
  delete lock.packages!['node_modules/no-data-pkg'];

  /** 将受影响包升级到 fixed 版本。 */
  lock.packages!['node_modules/alias-display'].version = '2.0.0';
  lock.packages![
    'node_modules/alias-display'
  ]!.resolved =
    'https://registry.npmjs.org/real-vuln-lib/-/real-vuln-lib-2.0.0.tgz';
  lock.packages!['node_modules/last-pkg'].version = '3.0.1';
  lock.packages![
    'node_modules/last-pkg'
  ]!.resolved =
    'https://registry.npmjs.org/last-pkg/-/last-pkg-3.0.1.tgz';

  const result = scanLockfile(lock, buildOptions());
  assert.equal(result.hostViolations.length, 0);
  assert.equal(result.closed, true);
  assert.equal(result.passed, true);
  assert.equal(result.errors.length, 0);
});

test('闭合但存在受影响版本时，closed 为 true 而 passed 为 false', () => {
  const lock = buildLock();
  delete lock.packages!['node_modules/evil-host-pkg'];
  const options = buildOptions();
  options.advisories['no-data-pkg'] = [];

  const result = scanLockfile(lock, options);
  assert.equal(result.closed, true);
  assert.equal(result.passed, false);
  assert.ok(
    result.evaluations.some((item) => item.affectedBy.length > 0),
  );
});

test('不支持的 lockfileVersion 直接不闭合', () => {
  const result = scanLockfile(
    { lockfileVersion: 2, packages: {} },
    { advisories: {} },
  );
  assert.equal(result.closed, false);
  assert.equal(result.passed, false);
});

test('parseLockfileText 解析合法 JSON，非法 JSON 抛错', () => {
  const lock = parseLockfileText(
    JSON.stringify({ lockfileVersion: 3, packages: {} }),
  );
  assert.equal(lock.lockfileVersion, 3);
  assert.throws(() => parseLockfileText('{not json'));
});

test('不可评估的公告事件导致该包未评估、整体不闭合', () => {
  const lock: PackageLock = {
    lockfileVersion: 3,
    packages: {
      'node_modules/broken-advisory-pkg': {
        version: '1.0.0',
        resolved:
          'https://registry.npmjs.org/broken-advisory-pkg/-/broken-advisory-pkg-1.0.0.tgz',
      },
    },
  };
  const options: ScanOptions = {
    advisories: {
      'broken-advisory-pkg': [
        advisory('OSV-BROKEN-1', 'broken-advisory-pkg', [
          { introduced: '0' },
          { fixed: 'not-a-version' },
        ]),
      ],
    },
  };
  const result = scanLockfile(lock, options);
  const evaluation = result.evaluations[0];
  assert.equal(evaluation.evaluated, false);
  assert.equal(evaluation.reason, 'invalid-advisory-event');
  assert.equal(result.closed, false);
});

test('预发布版本在 fixed 边界前仍受影响，正式版闭合', () => {
  const lock: PackageLock = {
    lockfileVersion: 3,
    packages: {
      'node_modules/prerelease-pkg': {
        version: '2.0.0-rc.1',
        resolved:
          'https://registry.npmjs.org/prerelease-pkg/-/prerelease-pkg-2.0.0-rc.1.tgz',
      },
    },
  };
  const options: ScanOptions = {
    advisories: {
      'prerelease-pkg': [
        advisory('OSV-PRERELEASE-1', 'prerelease-pkg', [
          { introduced: '0' },
          { fixed: '2.0.0' },
        ]),
      ],
    },
  };

  const rcResult = scanLockfile(lock, options);
  assert.equal(rcResult.closed, true);
  assert.equal(rcResult.passed, false);
  assert.deepEqual(rcResult.evaluations[0].affectedBy, ['OSV-PRERELEASE-1']);

  lock.packages!['node_modules/prerelease-pkg'].version = '2.0.0';
  lock.packages![
    'node_modules/prerelease-pkg'
  ]!.resolved =
    'https://registry.npmjs.org/prerelease-pkg/-/prerelease-pkg-2.0.0.tgz';
  const stableResult = scanLockfile(lock, options);
  assert.equal(stableResult.passed, true);
  assert.deepEqual(stableResult.evaluations[0].affectedBy, []);
});
