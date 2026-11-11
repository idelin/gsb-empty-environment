import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  BudgetExceededError,
  TemplateError,
  renderSafe,
  renderTemplate,
  sanitizeData,
  type Expr,
  type Node,
  type Template,
} from '../index.ts';

// ---------- AST 构造辅助 ----------

const text = (value: string): Node => ({ kind: 'text', value });
const v = (name: string): Node => ({ kind: 'var', name });
const lit = (value: string | number | boolean | null): Expr => ({
  kind: 'lit',
  value,
});
const field = (target: Expr, key: string): Expr => ({
  kind: 'field',
  target,
  key,
});
const list = (...items: Expr[]): Expr => ({ kind: 'list', items });
const iff = (
  cond: Expr,
  then: Node[],
  elseBranch: Node[] = [],
): Node => ({ kind: 'if', cond, then, else: elseBranch });
const forEach = (name: string, listExpr: Expr, body: Node[]): Node => ({
  kind: 'for',
  name,
  list: listExpr,
  body,
});
const call = (name: string, ...args: Expr[]): Node => ({
  kind: 'call',
  name,
  args,
});

// ---------- 基本渲染 ----------

test('文本、变量、字段、条件、循环、宏的基本渲染', () => {
  const template: Template = {
    macros: [
      {
        name: 'greet',
        params: ['who'],
        body: [text('Hello, '), v('who'), text('!')],
      },
    ],
    body: [
      call('greet', field(v('user'), 'name')),
      iff(field(v('user'), 'admin'), [text(' [admin]')]),
      forEach('item', field(v('user'), 'tags'), [
        text('<'),
        v('item'),
        text('>'),
      ]),
    ],
  };
  const out = renderTemplate(template, {
    user: { name: 'ada', admin: true, tags: ['x', 'y'] },
  });
  assert.equal(out, 'Hello, ada! [admin]<x><y>');
});

test('字段缺失与标量索引是严格错误', () => {
  assert.throws(
    () => renderTemplate({ body: [field(v('user'), 'nope')] }, { user: {} }),
    (e: unknown) => e instanceof TemplateError && e.code === 'UNKNOWN_FIELD',
  );
  assert.throws(
    () => renderTemplate({ body: [field(v('n'), 'x')] }, { n: 1 }),
    (e: unknown) => e instanceof TemplateError && e.code === 'TYPE_MISMATCH',
  );
});

// ---------- 递归宏 ----------

test('递归宏：沿链表数据自递归渲染', () => {
  const template: Template = {
    macros: [
      {
        name: 'renderList',
        params: ['node'],
        body: [
          field(v('node'), 'value'),
          iff(field(v('node'), 'next'), [
            text(','),
            call('renderList', field(v('node'), 'next')),
          ]),
        ],
      },
    ],
    body: [call('renderList', v('head'))],
  };
  const data = {
    head: {
      value: 'a',
      next: { value: 'b', next: { value: 'c', next: null } },
    },
  };
  assert.equal(renderTemplate(template, data), 'a,b,c');
});

test('互递归宏：宏在定义环境中彼此可见', () => {
  const template: Template = {
    macros: [
      {
        name: 'even',
        params: ['n'],
        body: [
          iff(v('n'), [
            text('E'),
            call('odd', lit(null)),
          ]),
        ],
      },
      {
        name: 'odd',
        params: ['n'],
        body: [text('O'), call('even', lit(null))],
      },
    ],
    body: [call('even', lit(true))],
  };
  // even(true) -> E, odd -> O, even(null) -> 终止
  assert.equal(renderTemplate(template), 'EO');
});

test('无限递归被调用深度预算截断，且不发布任何片段', () => {
  const template: Template = {
    macros: [
      {
        name: 'boom',
        params: [],
        body: [text('x'), call('boom')],
      },
    ],
    body: [call('boom')],
  };
  const result = renderSafe(template, {}, { maxCallDepth: 32 });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.ok(result.error instanceof BudgetExceededError);
    assert.equal(result.error.code, 'DEPTH_BUDGET_EXCEEDED');
  }
  // 结果类型上根本不存在 output 字段，片段无从泄漏。
  assert.equal('output' in result, false);
});

// ---------- 变量遮蔽与词法作用域 ----------

test('循环变量遮蔽全局同名变量，循环结束后恢复', () => {
  const template: Template = {
    body: [
      v('x'),
      forEach('x', list(lit('a'), lit('b')), [v('x')]),
      v('x'),
    ],
  };
  assert.equal(renderTemplate(template, { x: 'G' }), 'GabG');
});

test('宏参数遮蔽全局变量', () => {
  const template: Template = {
    macros: [{ name: 'm', params: ['x'], body: [v('x')] }],
    body: [call('m', lit('param')), v('x')],
  };
  assert.equal(renderTemplate(template, { x: 'global' }), 'paramglobal');
});

test('宏捕获定义环境：看不到调用方的循环局部变量', () => {
  const template: Template = {
    macros: [{ name: 'm', params: [], body: [v('x')] }],
    body: [forEach('x', list(lit('local')), [call('m')])],
  };
  // 宏体内的 x 解析到全局 x，而不是调用点的循环变量 x。
  assert.equal(renderTemplate(template, { x: 'global' }), 'global');
});

test('禁止动态查找：调用方独有的局部名在宏体内不可见', () => {
  const template: Template = {
    macros: [{ name: 'm', params: [], body: [v('loopvar')] }],
    body: [forEach('loopvar', list(lit('z')), [call('m')])],
  };
  assert.throws(
    () => renderTemplate(template, {}),
    (e: unknown) => e instanceof TemplateError && e.code === 'UNKNOWN_NAME',
  );
});

test('被局部遮蔽的宏名不再可调用', () => {
  const template: Template = {
    macros: [{ name: 'm', params: [], body: [text('M')] }],
    body: [forEach('m', list(lit('v')), [call('m')])],
  };
  assert.throws(
    () => renderTemplate(template, {}),
    (e: unknown) => e instanceof TemplateError && e.code === 'NOT_A_MACRO',
  );
});

// ---------- 保留属性名 ----------

test('保留属性名被丢弃，无法借字段访问触达宿主原型', () => {
  // JSON.parse 保证 __proto__ 是真正的自有键而非赋值陷阱。
  const data = JSON.parse(
    '{"__proto__":{"polluted":true},"constructor":"evil","prototype":"evil","safe":"ok"}',
  );
  assert.equal(renderTemplate({ body: [v('safe')] }, data), 'ok');

  for (const key of ['__proto__', 'constructor', 'prototype']) {
    assert.throws(
      () => renderTemplate({ body: [v(key)] }, data),
      (e: unknown) =>
        e instanceof TemplateError && e.code === 'UNKNOWN_NAME',
      `expected UNKNOWN_NAME for ${key}`,
    );
    assert.throws(
      () =>
        renderTemplate(
          { body: [field(v('rec'), key)] },
          { rec: JSON.parse(`{"${key}":"evil","a":1}`) },
        ),
      (e: unknown) =>
        e instanceof TemplateError && e.code === 'UNKNOWN_FIELD',
      `expected UNKNOWN_FIELD for ${key}`,
    );
  }
  // 宿主全局未被污染。
  assert.equal(({} as Record<string, unknown>).polluted, undefined);
});

test('记录上没有原型方法：hasOwnProperty/toString 等不可经字段解析', () => {
  const data = { rec: { a: 1 } };
  for (const key of ['hasOwnProperty', 'toString', 'valueOf', '__defineGetter__']) {
    assert.throws(
      () => renderTemplate({ body: [field(v('rec'), key)] }, data),
      (e: unknown) =>
        e instanceof TemplateError && e.code === 'UNKNOWN_FIELD',
      `expected UNKNOWN_FIELD for ${key}`,
    );
  }
});

// ---------- 恶意数据记录 ----------

test('带 getter 的数据被拒绝，且不会执行 getter', () => {
  let called = false;
  const evil = {
    get secret() {
      called = true;
      throw new Error('host code executed');
    },
  };
  assert.throws(
    () => renderTemplate({ body: [v('secret')] }, evil),
    (e: unknown) => e instanceof TemplateError && e.code === 'INVALID_DATA',
  );
  assert.equal(called, false);
});

test('Proxy 数据抛出的宿主异常被转换为 INVALID_DATA', () => {
  const proxy = new Proxy(
    {},
    {
      get() {
        throw new Error('proxy trap fired');
      },
      ownKeys() {
        throw new Error('proxy trap fired');
      },
    },
  );
  assert.throws(
    () => renderTemplate({ body: [text('hi')] }, proxy),
    (e: unknown) => e instanceof TemplateError && e.code === 'INVALID_DATA',
  );
});

test('函数、循环引用、类实例、非有限数字一律拒绝', () => {
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;
  const cases: unknown[] = [
    { fn: () => 1 },
    cyclic,
    { when: new Date(0) },
    { n: Number.NaN },
    { n: Number.POSITIVE_INFINITY },
    { s: Symbol('x') },
    { u: undefined },
  ];
  for (const data of cases) {
    assert.throws(
      () => renderTemplate({ body: [text('hi')] }, data),
      (e: unknown) => e instanceof TemplateError && e.code === 'INVALID_DATA',
    );
  }
});

test('渲染记录不会触发其 toString，直接报类型错误', () => {
  const data = { rec: { toString: 'PWNED' } };
  assert.throws(
    () => renderTemplate({ body: [v('rec')] }, data),
    (e: unknown) => e instanceof TemplateError && e.code === 'TYPE_MISMATCH',
  );
});

test('超大数组被数据节点预算拦截', () => {
  const data = { big: Array.from({ length: 5000 }, (_, i) => i) };
  assert.throws(
    () =>
      renderTemplate({ body: [text('hi')] }, data, { maxDataNodes: 100 }),
    (e: unknown) =>
      e instanceof BudgetExceededError && e.code === 'DATA_BUDGET_EXCEEDED',
  );
});

test('净化结果是无原型冻结记录', () => {
  const clean = sanitizeData(
    { a: [1, { b: 'x' }] },
    { maxDataNodes: 100, maxDataDepth: 10 },
  );
  const rec = clean as Record<string, unknown>;
  assert.equal(Object.getPrototypeOf(rec), null);
  assert.ok(Object.isFrozen(rec));
  const arr = rec.a as unknown[];
  assert.ok(Object.isFrozen(arr));
  assert.equal(Object.getPrototypeOf(arr[1]), null);
  assert.throws(() => {
    (rec as Record<string, unknown>).a = [];
  }, TypeError);
});

// ---------- 预算 ----------

test('输出字节预算按 UTF-8 计算，失败不发布片段', () => {
  const template: Template = {
    body: [text('你好世界'), text('再见')],
  };
  // “你好世界” = 12 字节，预算 12 时第二个文本超限。
  const result = renderSafe(template, {}, { maxOutputBytes: 12 });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error.code, 'OUTPUT_BUDGET_EXCEEDED');
  }
  assert.equal('output' in result, false);
  assert.equal(
    renderTemplate(template, {}, { maxOutputBytes: 18 }),
    '你好世界再见',
  );
});

test('循环迭代预算拦截恶意大数组', () => {
  const template: Template = {
    body: [forEach('x', v('big'), [v('x')])],
  };
  const data = { big: Array.from({ length: 1000 }, () => 'y') };
  const result = renderSafe(template, data, { maxIterations: 10 });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error.code, 'ITERATION_BUDGET_EXCEEDED');
  }
});

test('宏实参个数必须匹配', () => {
  const template: Template = {
    macros: [{ name: 'm', params: ['a', 'b'], body: [v('a')] }],
    body: [call('m', lit(1))],
  };
  assert.throws(
    () => renderTemplate(template),
    (e: unknown) =>
      e instanceof TemplateError && e.code === 'ARITY_MISMATCH',
  );
});
