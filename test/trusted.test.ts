import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PolicyRegistry,
  TrustedHTML,
  TrustedHtmlSink,
  type HtmlTarget,
} from '../index.ts';

const ATTACKER = '<img src=x onerror=alert(1)>';

function mockElement(): HtmlTarget {
  return { innerHTML: '' };
}

function setup() {
  const registry = new PolicyRegistry();
  const sanitizer = registry.createPolicy('sanitizer', (input) =>
    input.replace(/<script.*?<\/script>/gis, ''),
  );
  const formatter = registry.createPolicy(
    'formatter',
    (input) => `<b>${input}</b>`,
  );
  return { registry, sanitizer, formatter };
}

test('许可策略创建的值可以到达 innerHTML', () => {
  const { sanitizer } = setup();
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [sanitizer]);
  sink.setHTML(sanitizer.createHTML('<p>hello</p>'));
  assert.equal(el.innerHTML, '<p>hello</p>');
});

test('字符串拼接后品牌丢失，不得到达 innerHTML', () => {
  const { sanitizer } = setup();
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [sanitizer]);
  const trusted = sanitizer.createHTML('<p>safe</p>');

  const forms: unknown[] = [
    trusted + ATTACKER,
    `${trusted}${ATTACKER}`,
    String(trusted),
    trusted.toString(),
    trusted.toString().concat(ATTACKER),
  ];
  for (const form of forms) {
    assert.equal(typeof form, 'string');
    assert.throws(() => sink.setHTML(form), TypeError);
  }
  assert.equal(el.innerHTML, '');
});

test('TrustedHTML 无法被直接构造或伪造', () => {
  const { sanitizer } = setup();
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [sanitizer]);

  assert.throws(() => new TrustedHTML(Symbol('x'), ATTACKER), TypeError);

  const forgeries: unknown[] = [
    Object.create(TrustedHTML.prototype),
    { toString: () => ATTACKER },
    { name: 'sanitizer', value: ATTACKER },
  ];
  for (const fake of forgeries) {
    assert.throws(() => sink.setHTML(fake), TypeError);
  }
  assert.equal(el.innerHTML, '');
});

test('非许可策略的值不得到达 innerHTML', () => {
  const { sanitizer, formatter } = setup();
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [sanitizer]);

  const foreign = formatter.createHTML(ATTACKER);
  assert.throws(() => sink.setHTML(foreign), TypeError);
  assert.equal(el.innerHTML, '');
});

test('重复策略名在同一注册表中被拒绝', () => {
  const registry = new PolicyRegistry();
  registry.createPolicy('dup', (s) => s);
  assert.throws(() => registry.createPolicy('dup', (s) => s), /重复/);
});

test('不同注册表的同名策略不可互换', () => {
  const regA = new PolicyRegistry();
  const regB = new PolicyRegistry();
  const policyA = regA.createPolicy('same-name', (s) => s);
  const policyB = regB.createPolicy('same-name', (s) => s);

  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [policyA]);
  assert.throws(() => sink.setHTML(policyB.createHTML(ATTACKER)), TypeError);
  assert.equal(el.innerHTML, '');

  sink.setHTML(policyA.createHTML('<i>ok</i>'));
  assert.equal(el.innerHTML, '<i>ok</i>');
});

test('default 回退不扩大许可', () => {
  const { registry, sanitizer } = setup();

  // 未命中名字时回退到 default 策略
  const fallback = registry.getPolicy('does-not-exist');
  assert.equal(fallback, registry.defaultPolicy);
  assert.equal(fallback.name, 'default');
  assert.ok(fallback.isDefault);

  // default 策略会转义输出
  const escaped = fallback.createHTML(ATTACKER);
  assert.ok(!escaped.toString().includes('<img'));

  // 许可 sanitizer 的 sink 拒绝 default 品牌的值
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [sanitizer]);
  assert.throws(() => sink.setHTML(escaped), TypeError);
  assert.equal(el.innerHTML, '');

  // 显式许可 default 时才接受
  const el2 = mockElement();
  const defaultSink = new TrustedHtmlSink(el2, [registry.defaultPolicy]);
  defaultSink.setHTML(escaped);
  assert.ok(el2.innerHTML.length > 0);

  // default 是保留名，不能注册同名策略来窃取许可
  assert.throws(() => registry.createPolicy('default', (s) => s), /保留/);
});

test('合成策略只能解包声明的上游策略', () => {
  const { registry, sanitizer, formatter } = setup();
  const wrapper = registry.createComposedPolicy('wrapper', {
    from: [sanitizer],
    createHTML: (parts) =>
      parts.map((p) => (p.trusted ? `<div>${p.value}</div>` : '')).join(''),
  });

  // 声明的上游值可以合成，且合成结果带有 wrapper 自己的品牌
  const composed = wrapper.createHTML(sanitizer.createHTML('<span>x</span>'));
  const el = mockElement();
  const sink = new TrustedHtmlSink(el, [wrapper]);
  sink.setHTML(composed);
  assert.equal(el.innerHTML, '<div><span>x</span></div>');

  // 未声明的上游策略值被拒绝
  assert.throws(
    () => wrapper.createHTML(formatter.createHTML('y')),
    TypeError,
  );

  // 合成值的来源是 wrapper，不是上游 sanitizer
  const el2 = mockElement();
  const upstreamSink = new TrustedHtmlSink(el2, [sanitizer]);
  assert.throws(() => upstreamSink.setHTML(composed), TypeError);
  assert.equal(el2.innerHTML, '');
});
