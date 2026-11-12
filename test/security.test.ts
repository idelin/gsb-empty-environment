import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  contentAccessor,
  contentData,
  nativeAccessor,
  nativeData,
  ref,
} from '../src/graph.ts';
import { Xray } from '../src/xray.ts';
import { expandXray } from '../src/expand.ts';
import { createStandardRuntime, ID } from '../src/intrinsics.ts';
import { ContentMirror } from './content-mirror.ts';

const CONTENT = 'https://attacker.example';

test('Object.prototype 污染键（toString/hasOwnProperty 等）不会被当作原生属性', () => {
  const runtime = createStandardRuntime([{
    id: 'plain',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: { onlyOwn: nativeData(1) },
    contentProps: {},
  }]);
  const view = runtime.xray.view('plain') as Record<string, unknown>;

  for (const polluted of ['constructor', '__defineGetter__', '__lookupGetter__', 'isPrototypeOf']) {
    if (polluted === 'constructor') {
      assert.ok(view.constructor !== undefined, 'constructor comes from native prototype');
    } else {
      assert.equal(view[polluted], undefined, `${polluted} must not leak from Object.prototype`);
    }
  }
  assert.equal(view.onlyOwn, 1);
});

test('内容访问器在整个 Xray 读取过程中零执行，对照内容侧会执行', () => {
  let calls = 0;
  const runtime = createStandardRuntime([{
    id: 'booby',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: {
      safe: nativeData('ok'),
      reported: nativeAccessor(),
    },
    contentProps: {
      trap: contentAccessor({ get: () => { calls += 1; return 'x'; } }),
      safe: contentAccessor({ get: () => { calls += 1; return 'shadowed'; } }),
    },
  }]);
  const mirror = new ContentMirror(runtime.graph);
  const view = runtime.xray.view('booby') as Record<string, unknown>;

  void view.trap;
  void view.safe;
  void view.reported;
  'trap' in view;
  Object.keys(view);
  Object.getOwnPropertyDescriptors(view);
  expandXray(runtime.xray, 'booby');
  assert.equal(calls, 0);
  assert.equal(view.safe, 'ok');

  const contentView = mirror.mirror('booby') as Record<string, unknown>;
  assert.equal(contentView.trap, 'x');
  assert.equal(calls, 1);
});

test('视图身份跨多次读取稳定，函数视图也被识别为 Xray 视图', () => {
  const runtime = createStandardRuntime([{
    id: 'holder',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: {
      method: nativeData(ref(ID.ObjectToString)),
      self: nativeData(ref('holder')),
    },
    contentProps: {},
  }]);
  const view = runtime.xray.view('holder') as Record<string, unknown>;

  assert.equal(view.method, (view.method as unknown));
  assert.equal(view.self, view);
  assert.ok(runtime.xray.isView(view.method));
  assert.equal(runtime.xray.nodeIdOf(view.method as object), ID.ObjectToString);
  assert.equal(typeof view.method, 'function');
});

test('环快照在任意深度下终止，且回边标注来源节点', () => {
  const runtime = createStandardRuntime([
    {
      id: 'a',
      domain: CONTENT,
      intrinsic: 'Object',
      nativeProto: ID.ObjectPrototype,
      nativeProps: { next: nativeData(ref('b')) },
      contentProps: {},
    },
    {
      id: 'b',
      domain: CONTENT,
      intrinsic: 'Object',
      nativeProto: ID.ObjectPrototype,
      nativeProps: { next: nativeData(ref('a')) },
      contentProps: {},
    },
  ]);

  for (const depth of [1, 2, 5, 50]) {
    const snapshot = expandXray(runtime.xray, 'a', { maxDepth: depth });
    const text = JSON.stringify(snapshot);
    assert.ok(text.includes('"$cycle":"a"') || text.includes('"$depthLimit"'));
    assert.doesNotThrow(() => JSON.parse(text));
  }
});

test('数组下标遮蔽与原生方法遮蔽均不影响视图', () => {
  const runtime = createStandardRuntime([{
    id: 'arr',
    domain: CONTENT,
    intrinsic: 'Array',
    nativeProto: ID.ArrayPrototype,
    nativeProps: {},
    contentProps: {
      '1': contentData('evil'),
      join: contentData(99),
    },
    arraySlots: ['a', 'b', 'c'],
  }]);
  const view = runtime.xray.view('arr') as unknown as {
    readonly length: number;
    readonly [index: number]: unknown;
    join: (separator?: string) => string;
  };

  assert.equal(view[1], 'b');
  assert.equal(view.length, 3);
  assert.equal(typeof view.join, 'function');
  assert.equal(view.join('|'), 'a|b|c');
});

test('非法图在构造时被拒绝', () => {
  assert.throws(() => new Xray({
    nodes: {
      x: {
        id: 'x',
        domain: CONTENT,
        intrinsic: 'Object',
        nativeProps: { link: nativeData(ref('ghost')) },
      },
    },
  }), /missing node/);

  assert.throws(() => new Xray({
    nodes: {
      x: { id: 'x', domain: CONTENT, intrinsic: 'Object', nativeProto: 'ghost' },
    },
  }), /missing node/);
});
