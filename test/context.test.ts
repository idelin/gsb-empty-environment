import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  assertContextAllowed,
  checkHostClassQuery,
  PermissionDeniedError,
  validateContextPermissions,
  type ContextOptions,
} from '../index.ts';

const SAFE_ALLOW_LIST = ['com.acme.api.', 'com.acme.util.MathHelper'];

const COMPLIANT_OPTIONS: ContextOptions = {
  allowAllAccess: false,
  allowNativeAccess: false,
  hostAccess: 'explicit',
  allowCreateThread: false,
  resourceLimits: null,
};

describe('validateContextPermissions 硬性拒绝条件', () => {
  it('allowAllAccess 开启一律拒绝', () => {
    const result = validateContextPermissions(
      { ...COMPLIANT_OPTIONS, allowAllAccess: true },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.includes('allowAllAccess')));
  });

  it('allowNativeAccess 开启一律拒绝', () => {
    const result = validateContextPermissions(
      { ...COMPLIANT_OPTIONS, allowNativeAccess: true },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.includes('allowNativeAccess')));
  });

  it("hostAccess 选 'all' 一律拒绝", () => {
    const result = validateContextPermissions(
      { ...COMPLIANT_OPTIONS, hostAccess: 'all' },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.includes('HostAccess.ALL')));
  });

  it('允许创建线程但未设资源上限一律拒绝', () => {
    const result = validateContextPermissions(
      { ...COMPLIANT_OPTIONS, allowCreateThread: true, resourceLimits: null },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.includes('资源上限')));
  });

  it('资源上限为空对象或零值时仍拒绝', () => {
    for (const resourceLimits of [
      {},
      { statementLimit: 0 },
      { statementLimit: null },
      { statementLimit: Number.NaN },
    ]) {
      const result = validateContextPermissions(
        { ...COMPLIANT_OPTIONS, allowCreateThread: true, resourceLimits },
        SAFE_ALLOW_LIST,
      );
      assert.equal(result.allowed, false, `应拒绝: ${JSON.stringify(resourceLimits)}`);
    }
  });

  it('多个危险开关同时打开时聚合所有原因', () => {
    const result = validateContextPermissions(
      {
        allowAllAccess: true,
        allowNativeAccess: true,
        hostAccess: 'all',
        allowCreateThread: true,
      },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.length >= 4);
  });
});

describe('validateContextPermissions 放行条件', () => {
  it('合规配置通过并回传统一清单', () => {
    const result = validateContextPermissions(COMPLIANT_OPTIONS, SAFE_ALLOW_LIST);
    assert.equal(result.allowed, true);
    assert.deepEqual(result.reasons, []);
    assert.deepEqual(result.allowList, SAFE_ALLOW_LIST);
  });

  it('允许创建线程但配置了正数 statementLimit 时通过', () => {
    const result = validateContextPermissions(
      {
        ...COMPLIANT_OPTIONS,
        allowCreateThread: true,
        resourceLimits: { statementLimit: 1000 },
      },
      SAFE_ALLOW_LIST,
    );
    assert.equal(result.allowed, true);
  });

  it('空白清单条目去重，非法条目导致拒绝', () => {
    const result = validateContextPermissions(COMPLIANT_OPTIONS, [
      'com.acme.api.',
      'com.acme.api.',
      'sun.misc.Unsafe',
    ]);
    assert.equal(result.allowed, false);
    assert.deepEqual(result.allowList, ['com.acme.api.']);
    assert.ok(result.reasons.some((r) => r.includes('sun.')));
  });

  it('带空白的禁止包条目不能借 trim 绕过校验', () => {
    const result = validateContextPermissions(COMPLIANT_OPTIONS, [
      '  sun.misc.Unsafe  ',
      ' java.lang.reflect.',
    ]);
    assert.equal(result.allowed, false);
    assert.deepEqual(result.allowList, []);
  });
});

describe('assertContextAllowed', () => {
  it('合规时不抛异常', () => {
    assert.doesNotThrow(() =>
      assertContextAllowed(COMPLIANT_OPTIONS, SAFE_ALLOW_LIST),
    );
  });

  it('违规时抛 PermissionDeniedError 并携带原因', () => {
    assert.throws(
      () =>
        assertContextAllowed(
          { ...COMPLIANT_OPTIONS, allowAllAccess: true },
          SAFE_ALLOW_LIST,
        ),
      (error: unknown) => {
        assert.ok(error instanceof PermissionDeniedError);
        assert.ok(error.reasons.length > 0);
        assert.ok(error.message.includes('权限校验失败'));
        return true;
      },
    );
  });
});

describe('checkHostClassQuery 集成裁决', () => {
  it('合规清单下的包内类通过并返回解析名', () => {
    const decision = checkHostClassQuery(
      'com.acme.api.Outer$Inner',
      SAFE_ALLOW_LIST,
    );
    assert.equal(decision.allowed, true);
    assert.equal(decision.resolved, 'com.acme.api.Outer');
  });

  it('数组类按元素类型裁决', () => {
    const allowed = checkHostClassQuery('[Lcom.acme.api.Foo;', SAFE_ALLOW_LIST);
    assert.equal(allowed.allowed, true);

    const denied = checkHostClassQuery('[Lcom.acme.api2.Evil;', SAFE_ALLOW_LIST);
    assert.equal(denied.allowed, false);
    assert.ok(denied.reasons.length > 0);
  });

  it('Runtime 与 ProcessBuilder 查询被拒绝', () => {
    for (const name of [
      'java.lang.Runtime',
      'java.lang.ProcessBuilder',
      'java.lang.Runtime$1',
      '[Ljava.lang.ProcessBuilder;',
    ]) {
      assert.equal(checkHostClassQuery(name, SAFE_ALLOW_LIST).allowed, false, name);
    }
  });

  it('reflect/invoke/sun 查询即使显式列入也拒绝', () => {
    const list = [
      'java.lang.reflect.Method',
      'java.lang.invoke.MethodHandles',
      'sun.misc.Unsafe',
    ];
    for (const name of list) {
      assert.equal(checkHostClassQuery(name, list).allowed, false, name);
    }
  });
});
