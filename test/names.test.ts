import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  isForbiddenClass,
  isHostClassAllowed,
  matchesAllowList,
  resolveHostClassName,
  validateAllowListEntry,
} from '../index.ts';

const SAFE_ALLOW_LIST = [
  'com.acme.api.',
  'com.acme.util.MathHelper',
  'com.acme.safe.',
];

describe('resolveHostClassName', () => {
  it('原样返回普通二进制名', () => {
    assert.deepEqual(resolveHostClassName('com.acme.api.Foo'), {
      ok: true,
      canonical: 'com.acme.api.Foo',
    });
  });

  it('内部类截断到外部类', () => {
    assert.deepEqual(
      resolveHostClassName('com.acme.api.Outer$Inner'),
      { ok: true, canonical: 'com.acme.api.Outer' },
    );
    assert.deepEqual(
      resolveHostClassName('com.acme.api.Outer$Deep$Nest'),
      { ok: true, canonical: 'com.acme.api.Outer' },
    );
  });

  it('匿名内部类同样截断', () => {
    assert.deepEqual(
      resolveHostClassName('com.acme.api.Outer$1'),
      { ok: true, canonical: 'com.acme.api.Outer' },
    );
  });

  it('数组描述符解析到元素类型', () => {
    assert.deepEqual(
      resolveHostClassName('[Lcom.acme.api.Foo;'),
      { ok: true, canonical: 'com.acme.api.Foo' },
    );
    assert.deepEqual(
      resolveHostClassName('[[Lcom.acme.api.Foo;'),
      { ok: true, canonical: 'com.acme.api.Foo' },
    );
  });

  it('数组加内部类先取元素再取外部类', () => {
    assert.deepEqual(
      resolveHostClassName('[Lcom.acme.api.Outer$Inner;'),
      { ok: true, canonical: 'com.acme.api.Outer' },
    );
  });

  it('基本类型数组不映射到宿主类', () => {
    assert.equal(resolveHostClassName('[I').ok, false);
    assert.equal(resolveHostClassName('[[Z').ok, false);
  });

  it('拒绝畸形输入', () => {
    for (const bad of ['', '   ', '[Lcom.acme.Foo', '$Inner', 'com..Foo', 'com/acme/Foo']) {
      assert.equal(resolveHostClassName(bad).ok, false, `应拒绝: ${bad}`);
    }
  });
});

describe('包前缀按包边界匹配（防同前缀绕过）', () => {
  it('放行包及其子包', () => {
    assert.equal(
      isHostClassAllowed('com.acme.api.Foo', SAFE_ALLOW_LIST),
      true,
    );
    assert.equal(
      isHostClassAllowed('com.acme.api.spi.Bar', SAFE_ALLOW_LIST),
      true,
    );
  });

  it('同前缀的另一个包不放行', () => {
    assert.equal(
      isHostClassAllowed('com.acme.api2.Evil', SAFE_ALLOW_LIST),
      false,
    );
    assert.equal(
      isHostClassAllowed('com.acme.apix.Evil', SAFE_ALLOW_LIST),
      false,
    );
    assert.equal(
      isHostClassAllowed('com.acme.apimodel.Thing', SAFE_ALLOW_LIST),
      false,
    );
  });

  it('精确类名条目只匹配该类本身', () => {
    assert.equal(
      isHostClassAllowed('com.acme.util.MathHelper', SAFE_ALLOW_LIST),
      true,
    );
    assert.equal(
      isHostClassAllowed('com.acme.util.MathHelperExtra', SAFE_ALLOW_LIST),
      false,
    );
    assert.equal(
      isHostClassAllowed('com.acme.util.Other', SAFE_ALLOW_LIST),
      false,
    );
  });

  it('默认拒绝：清单为空时任何类都拿不到', () => {
    assert.equal(isHostClassAllowed('com.acme.api.Foo', []), false);
    assert.equal(matchesAllowList('com.acme.api.Foo', []), false);
  });
});

describe('内部类继承外部类放行', () => {
  it('外部类放行时内部类与匿名类放行', () => {
    assert.equal(
      isHostClassAllowed('com.acme.api.Outer$Inner', SAFE_ALLOW_LIST),
      true,
    );
    assert.equal(
      isHostClassAllowed('com.acme.api.Outer$1$2', SAFE_ALLOW_LIST),
      true,
    );
  });

  it('外部类未放行时内部类不能借 $ 名绕过', () => {
    assert.equal(
      isHostClassAllowed('com.acme.hidden.Outer$Inner', SAFE_ALLOW_LIST),
      false,
    );
  });
});

describe('数组类只看元素类型', () => {
  it('元素类型放行则数组放行', () => {
    assert.equal(
      isHostClassAllowed('[Lcom.acme.api.Foo;', SAFE_ALLOW_LIST),
      true,
    );
    assert.equal(
      isHostClassAllowed('[[[Lcom.acme.api.Foo;', SAFE_ALLOW_LIST),
      true,
    );
  });

  it('元素类型未放行则数组拒绝', () => {
    assert.equal(
      isHostClassAllowed('[Lcom.acme.api2.Evil;', SAFE_ALLOW_LIST),
      false,
    );
    assert.equal(
      isHostClassAllowed('[Ljava.lang.Runtime;', SAFE_ALLOW_LIST),
      false,
    );
  });
});

describe('禁止包永不放行', () => {
  const forbiddenNames = [
    'java.lang.reflect.Method',
    'java.lang.invoke.MethodHandles',
    'sun.misc.Unsafe',
    'sun.reflect.annotation.AnnotationParser',
  ];

  for (const name of forbiddenNames) {
    it(`拒绝 ${name}`, () => {
      assert.equal(isForbiddenClass(name), true);
      assert.equal(isHostClassAllowed(name, [name, 'sun.', 'java.lang.reflect.']), false);
    });
  }

  it('禁止包的内部类与数组同样拒绝', () => {
    assert.equal(
      isHostClassAllowed('sun.misc.Unsafe$1', ['sun.misc.Unsafe']),
      false,
    );
    assert.equal(
      isHostClassAllowed('[Ljava.lang.invoke.MethodHandles;', ['java.lang.invoke.']),
      false,
    );
  });

  it('java.lang 本身不在禁止范围', () => {
    assert.equal(isForbiddenClass('java.lang.String'), false);
  });

  it('清单中的禁止包条目被判定为非法', () => {
    assert.ok(validateAllowListEntry('sun.') !== null);
    assert.ok(validateAllowListEntry('java.lang.reflect.Method') !== null);
  });
});

describe('高危宿主类默认拿不到', () => {
  it('Runtime 与 ProcessBuilder 在安全清单下拒绝', () => {
    assert.equal(isHostClassAllowed('java.lang.Runtime', SAFE_ALLOW_LIST), false);
    assert.equal(isHostClassAllowed('java.lang.ProcessBuilder', SAFE_ALLOW_LIST), false);
    assert.equal(
      isHostClassAllowed('[Ljava.lang.ProcessBuilder;', SAFE_ALLOW_LIST),
      false,
    );
  });
});

describe('validateAllowListEntry', () => {
  it('接受合法的包前缀与类名', () => {
    assert.equal(validateAllowListEntry('com.acme.api.'), null);
    assert.equal(validateAllowListEntry('com.acme.util.MathHelper'), null);
  });

  it('拒绝数组、内部类、空白与畸形条目', () => {
    assert.ok(validateAllowListEntry('[Lcom.acme.Foo;') !== null);
    assert.ok(validateAllowListEntry('com.acme.Outer$Inner') !== null);
    assert.ok(validateAllowListEntry('') !== null);
    assert.ok(validateAllowListEntry('com..acme.') !== null);
    assert.ok(validateAllowListEntry(42) !== null);
  });
});
