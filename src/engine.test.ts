import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import { inferProtectionClasses } from './engine.ts';
import { InferenceError } from './types.ts';
import type {
  InferenceInput,
  Operation,
  PathRule,
  ProtectionClass,
} from './types.ts';
import {
  isStronger,
  isWeaker,
  matchesPrefix,
  meetsRequirement,
  normalizePath,
  strongestRequirement,
  strongerOf,
} from './index.ts';

const RULES: PathRule[] = [
  { prefix: '/var/mobile/Containers/secret', minClass: 'complete' },
];
const DEFAULT: ProtectionClass =
  'completeUntilFirstUserAuthentication';

function run(
  operations: Operation[],
  rules: PathRule[] = RULES,
  defaultClass: ProtectionClass = DEFAULT,
) {
  const input: InferenceInput = { operations, rules, defaultClass };
  return inferProtectionClasses(input);
}

function resultFor(
  result: ReturnType<typeof inferProtectionClasses>,
  path: string,
) {
  const found = result.paths.find((entry) => entry.path === path);
  assert.ok(found, `expected result for path ${path}`);
  return found;
}

describe('保护类强弱', () => {
  it('complete 最强、none 最弱，顺序严格递减', () => {
    assert.equal(isStronger('complete', 'completeUnlessOpen'), true);
    assert.equal(
      isStronger(
        'completeUnlessOpen',
        'completeUntilFirstUserAuthentication',
      ),
      true,
    );
    assert.equal(
      isStronger('completeUntilFirstUserAuthentication', 'none'),
      true,
    );
    assert.equal(isWeaker('none', 'complete'), true);
    assert.equal(isStronger('none', 'complete'), false);
  });

  it('meetsRequirement 与 strongerOf 语义正确', () => {
    assert.equal(meetsRequirement('complete', 'complete'), true);
    assert.equal(meetsRequirement('completeUnlessOpen', 'complete'), false);
    assert.equal(
      meetsRequirement('none', 'completeUntilFirstUserAuthentication'),
      false,
    );
    assert.equal(
      strongerOf('none', 'completeUnlessOpen'),
      'completeUnlessOpen',
    );
  });
});

describe('路径规则匹配', () => {
  it('边界感知前缀匹配，避免 /ab 误命中 /a', () => {
    assert.equal(matchesPrefix('/a', '/a'), true);
    assert.equal(matchesPrefix('/a/b', '/a'), true);
    assert.equal(matchesPrefix('/ab', '/a'), false);
    assert.equal(normalizePath('/a/'), '/a');
    assert.equal(matchesPrefix('/a/b', '/a/'), true);
  });

  it('多条规则命中时取最强要求', () => {
    const rules: PathRule[] = [
      { prefix: '/sec', minClass: 'completeUntilFirstUserAuthentication' },
      { prefix: '/sec/deep', minClass: 'complete' },
      { prefix: '/other', minClass: 'none' },
    ];
    assert.equal(strongestRequirement('/sec/deep/file', rules), 'complete');
    assert.equal(
      strongestRequirement('/sec/file', rules),
      'completeUntilFirstUserAuthentication',
    );
    assert.equal(strongestRequirement('/unmatched', rules), undefined);
  });
});

describe('create 与缺省默认类', () => {
  it('create 显式 complete 满足规则且无违规', () => {
    const result = run([
      {
        op: 'create',
        path: '/var/mobile/Containers/secret/key',
        protectionClass: 'complete',
      },
    ]);
    const entry = resultFor(
      result,
      '/var/mobile/Containers/secret/key',
    );
    assert.equal(entry.finalClass, 'complete');
    assert.equal(entry.compliant, true);
    assert.equal(entry.requiredClass, 'complete');
    assert.equal(result.violations.length, 0);
  });

  it('缺省 create 取应用默认类', () => {
    const result = run([{ op: 'create', path: '/tmp/plain' }]);
    assert.equal(resultFor(result, '/tmp/plain').finalClass, DEFAULT);
    assert.equal(result.violations.length, 0);
  });
});

describe('场景一：create complete 后缺省 atomicWrite', () => {
  it('原子写缺省回落到默认类且不继承原类，定位到 atomicWrite 步', () => {
    const path = '/var/mobile/Containers/secret/wallet.dat';
    const result = run([
      { op: 'create', path, protectionClass: 'complete', id: 'c1' },
      { op: 'atomicWrite', path, id: 'aw1' },
    ]);

    const entry = resultFor(result, path);
    assert.equal(entry.finalClass, DEFAULT);
    assert.equal(entry.compliant, false);
    assert.equal(entry.decidingOperationIndex, 1);
    assert.equal(entry.decidingOperationId, 'aw1');

    assert.equal(result.violations.length, 1);
    const violation = result.violations[0];
    assert.equal(violation.path, path);
    assert.equal(violation.requiredClass, 'complete');
    assert.equal(violation.actualClass, DEFAULT);
    assert.equal(violation.violatingOperation, 'atomicWrite');
    assert.equal(violation.violatingOperationIndex, 1);
    assert.equal(violation.violatingOperationId, 'aw1');
    assert.match(violation.reason, /atomicWrite/);
  });

  it('显式 complete 的 atomicWrite 不产生降级', () => {
    const path = '/var/mobile/Containers/secret/wallet.dat';
    const result = run([
      { op: 'create', path, protectionClass: 'complete' },
      { op: 'atomicWrite', path, protectionClass: 'complete' },
    ]);
    assert.equal(resultFor(result, path).finalClass, 'complete');
    assert.equal(result.violations.length, 0);
  });
});

describe('场景二：copy 副本取默认类', () => {
  it('copy 进规则目录触发违规，定位到 copy 步而非源文件', () => {
    const source = '/var/mobile/Containers/secret/source.key';
    const destination = '/var/mobile/Containers/secret/copy.key';
    const result = run([
      { op: 'create', path: source, protectionClass: 'complete', id: 'src' },
      { op: 'copy', from: source, path: destination, id: 'cp1' },
    ]);

    assert.equal(resultFor(result, source).finalClass, 'complete');
    const copied = resultFor(result, destination);
    assert.equal(copied.finalClass, DEFAULT);
    assert.equal(copied.compliant, false);
    assert.equal(copied.decidingOperationIndex, 1);

    const violation = result.violations.find(
      (item) => item.path === destination,
    );
    assert.ok(violation);
    assert.equal(violation.violatingOperation, 'copy');
    assert.equal(violation.violatingOperationIndex, 1);
    assert.equal(violation.violatingOperationId, 'cp1');
  });

  it('copy 后紧跟 setAttributes 提升到 complete，则合规', () => {
    const source = '/var/mobile/Containers/secret/source.key';
    const destination = '/var/mobile/Containers/secret/copy.key';
    const result = run([
      { op: 'create', path: source, protectionClass: 'complete' },
      { op: 'copy', from: source, path: destination, id: 'cp1' },
      {
        op: 'setAttributes',
        path: destination,
        protectionClass: 'complete',
        id: 'sa1',
      },
    ]);

    const copied = resultFor(result, destination);
    assert.equal(copied.finalClass, 'complete');
    assert.equal(copied.compliant, true);
    assert.equal(copied.decidingOperationIndex, 2);
    assert.equal(copied.decidingOperationId, 'sa1');
    assert.equal(result.violations.length, 0);
  });

  it('copy 与 setAttributes 之间插入其它操作时，终态由后续设类决定', () => {
    const source = '/var/mobile/Containers/secret/source.key';
    const destination = '/var/mobile/Containers/secret/copy.key';
    const result = run([
      { op: 'create', path: source, protectionClass: 'complete' },
      { op: 'copy', from: source, path: destination },
      {
        op: 'create',
        path: '/var/mobile/Containers/secret/other',
        protectionClass: 'complete',
      },
      { op: 'setAttributes', path: destination, protectionClass: 'complete' },
    ]);
    assert.equal(resultFor(result, destination).finalClass, 'complete');
    assert.equal(result.violations.length, 0);
  });
});

describe('场景三：restore 一律取默认类', () => {
  it('恢复强类文件后回落到默认类，定位到 restore 步', () => {
    const path = '/var/mobile/Containers/secret/backup.db';
    const result = run([
      { op: 'create', path, protectionClass: 'complete', id: 'c1' },
      { op: 'restore', path, id: 'rs1' },
    ]);

    const entry = resultFor(result, path);
    assert.equal(entry.finalClass, DEFAULT);
    assert.equal(entry.compliant, false);
    assert.equal(entry.decidingOperationIndex, 1);

    const violation = result.violations[0];
    assert.equal(violation.violatingOperation, 'restore');
    assert.equal(violation.violatingOperationIndex, 1);
    assert.equal(violation.violatingOperationId, 'rs1');
    assert.match(violation.reason, /restore/);
  });
});

describe('场景四：move 进受规则目录', () => {
  it('弱类文件 move 进规则目录保留弱类，定位 move 并溯源到最初设类', () => {
    const source = '/tmp/inbox/weak.txt';
    const destination = '/var/mobile/Containers/secret/moved.txt';
    const result = run([
      { op: 'create', path: source, id: 'weak-create' },
      { op: 'move', from: source, path: destination, id: 'mv1' },
    ]);

    assert.equal(
      result.paths.some((entry) => entry.path === source),
      false,
    );
    const moved = resultFor(result, destination);
    assert.equal(moved.finalClass, DEFAULT);
    assert.equal(moved.compliant, false);
    assert.equal(moved.decidingOperationIndex, 1);

    const violation = result.violations[0];
    assert.equal(violation.violatingOperation, 'move');
    assert.equal(violation.violatingOperationIndex, 1);
    assert.equal(violation.violatingOperationId, 'mv1');
    assert.equal(violation.moveSourcePath, source);
    assert.equal(violation.originOperationIndex, 0);
    assert.equal(violation.originOperationId, 'weak-create');
    assert.match(violation.reason, /move/);
    assert.match(violation.reason, /第 0 步/);
  });

  it('强类文件 move 进规则目录保留强类，不违规', () => {
    const source = '/tmp/inbox/strong.txt';
    const destination = '/var/mobile/Containers/secret/strong-moved.txt';
    const result = run([
      { op: 'create', path: source, protectionClass: 'complete' },
      { op: 'move', from: source, path: destination },
    ]);
    assert.equal(resultFor(result, destination).finalClass, 'complete');
    assert.equal(result.violations.length, 0);
  });

  it('move 后再 setAttributes 修正，终态合规', () => {
    const source = '/tmp/inbox/weak2.txt';
    const destination = '/var/mobile/Containers/secret/moved2.txt';
    const result = run([
      { op: 'create', path: source },
      { op: 'move', from: source, path: destination, id: 'mv1' },
      {
        op: 'setAttributes',
        path: destination,
        protectionClass: 'complete',
      },
    ]);
    assert.equal(resultFor(result, destination).finalClass, 'complete');
    assert.equal(result.violations.length, 0);
  });
});

describe('多路径与综合序列', () => {
  it('按序推导每个路径最终类，仅报告终态弱于要求者', () => {
    const result = run(
      [
        { op: 'create', path: '/sec/a', protectionClass: 'complete' },
        { op: 'create', path: '/sec/b', protectionClass: 'none' },
        { op: 'atomicWrite', path: '/sec/a' },
        { op: 'setAttributes', path: '/sec/b', protectionClass: 'complete' },
      ],
      [{ prefix: '/sec', minClass: 'complete' }],
    );

    const a = resultFor(result, '/sec/a');
    const b = resultFor(result, '/sec/b');
    assert.equal(a.finalClass, DEFAULT);
    assert.equal(a.compliant, false);
    assert.equal(b.finalClass, 'complete');
    assert.equal(b.compliant, true);
    assert.equal(result.violations.length, 1);
    assert.equal(result.violations[0].path, '/sec/a');
  });

  it('copy 覆盖已存在目标时，目标类被默认类替换而非保留', () => {
    const result = run(
      [
        { op: 'create', path: '/sec/dst', protectionClass: 'complete' },
        { op: 'create', path: '/tmp/src' },
        { op: 'copy', from: '/tmp/src', path: '/sec/dst' },
      ],
      [{ prefix: '/sec', minClass: 'complete' }],
    );

    const dst = resultFor(result, '/sec/dst');
    assert.equal(dst.finalClass, DEFAULT);
    assert.equal(dst.decidingOperationIndex, 2);
    assert.equal(result.violations[0].violatingOperation, 'copy');
  });

  it('无规则命中的路径不产生违规，requiredClass 为空', () => {
    const result = run([
      { op: 'create', path: '/tmp/a', protectionClass: 'none' },
      { op: 'restore', path: '/tmp/a' },
    ]);
    const entry = resultFor(result, '/tmp/a');
    assert.equal(entry.finalClass, DEFAULT);
    assert.equal(entry.requiredClass, undefined);
    assert.equal(entry.compliant, true);
    assert.equal(result.violations.length, 0);
  });
});

describe('输入校验', () => {
  it('copy/move 引用不存在的源路径时抛 InferenceError 并带操作下标', () => {
    assert.throws(
      () => run([{ op: 'copy', from: '/nope', path: '/sec/x' }]),
      (error: unknown) => {
        assert.ok(error instanceof InferenceError);
        assert.equal(error.operationIndex, 0);
        assert.equal(error.operationType, 'copy');
        return true;
      },
    );

    assert.throws(
      () => run([{ op: 'move', from: '/nope', path: '/sec/x' }]),
      (error: unknown) => {
        assert.ok(error instanceof InferenceError);
        assert.equal(error.operationIndex, 0);
        assert.equal(error.operationType, 'move');
        return true;
      },
    );
  });

  it('setAttributes 缺保护类或目标不存在时抛 InferenceError', () => {
    assert.throws(
      () =>
        inferProtectionClasses({
          operations: [
            { op: 'setAttributes', path: '/sec/x' } as Operation,
          ],
          rules: [],
          defaultClass: DEFAULT,
        }),
      (error: unknown) => {
        assert.ok(error instanceof InferenceError);
        assert.equal(error.operationType, 'setAttributes');
        return true;
      },
    );

    assert.throws(
      () =>
        run([
          {
            op: 'setAttributes',
            path: '/sec/ghost',
            protectionClass: 'complete',
          },
        ]),
      (error: unknown) => error instanceof InferenceError,
    );
  });

  it('空操作序列返回空结果', () => {
    const result = run([]);
    assert.deepEqual(result.paths, []);
    assert.deepEqual(result.violations, []);
  });
});
