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
import { createStandardRuntime, ID, standardGraph } from '../src/intrinsics.ts';
import { ContentMirror } from './content-mirror.ts';

const CONTENT = 'https://attacker.example';

let contentGetterCalls = 0;
let shadowedToStringCalls = 0;
let fakeProtoProbeCalls = 0;

const buildGraph = () => {
  const evilProto = {
    id: 'evilProto',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    contentProto: null,
    nativeProps: {},
    contentProps: {
      stolen: contentData('from-evil-proto'),
      probe: contentAccessor({ get: () => { fakeProtoProbeCalls += 1; return 'proto-getter'; } }),
    },
  };

  const evildoer = {
    id: 'evildoer',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    // 内容域把原型替换成自己的对象。
    contentProto: 'evilProto',
    nativeProps: {
      safeName: nativeData('legit'),
      nativeOnly: nativeData(42),
      shadowed: nativeData('native-value'),
      nativeGetter: nativeAccessor({ enumerable: true, configurable: true }),
    },
    contentProps: {
      // 遮蔽原型链上的原生 toString。
      toString: contentAccessor({ get: () => () => { shadowedToStringCalls += 1; return 'pwned'; } }),
      // 追加的内容 getter。
      loot: contentAccessor({ get: () => { contentGetterCalls += 1; return 'secret'; } }),
      // 遮蔽原生数据属性。
      shadowed: contentData('content-value'),
      extra: contentData('invisible'),
    },
  };

  const left = {
    id: 'left',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: { name: nativeData('left'), partner: nativeData(ref('right')) },
    contentProps: {},
  };
  const right = {
    id: 'right',
    domain: CONTENT,
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: { name: nativeData('right'), partner: nativeData(ref('left')) },
    contentProps: {},
  };

  const list = {
    id: 'list',
    domain: CONTENT,
    intrinsic: 'Array',
    nativeProto: ID.ArrayPrototype,
    nativeProps: {
      length: nativeData(3, { enumerable: false, configurable: false, writable: true }),
    },
    contentProps: {
      // 内容域还遮蔽了数组下标与 join。
      '0': contentData('shadowed-slot'),
      join: contentData('not-a-function'),
    },
    arraySlots: [ref('evildoer'), 'two', ref('left')],
  };

  const privileged = {
    id: 'privilegedObj',
    domain: 'privileged',
    intrinsic: 'Object',
    nativeProto: ID.ObjectPrototype,
    nativeProps: { token: nativeData('trusted') },
    contentProps: {},
  };

  return standardGraph([evilProto, evildoer, left, right, list, privileged]);
};

const setup = () => {
  contentGetterCalls = 0;
  shadowedToStringCalls = 0;
  fakeProtoProbeCalls = 0;
  const graph = buildGraph();
  const xray = new Xray(graph);
  const mirror = new ContentMirror(graph);
  return { graph, xray, mirror };
};

test('遮蔽的 toString：视图走原生实现，内容 getter 不执行', () => {
  const { xray, mirror } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;

  assert.equal(typeof view.toString, 'function');
  assert.equal((view.toString as () => string).call(view), '[object Object]');
  assert.equal(shadowedToStringCalls, 0);

  // 对照：内容域语义下确实会拿到攻击者的函数。
  const contentView = mirror.mirror('evildoer') as Record<string, () => string>;
  assert.equal(contentView.toString!(), 'pwned');
  assert.equal(shadowedToStringCalls, 1);

  assert.equal(view.isContentShadowed, undefined);
  assert.ok(xray.isContentShadowed(view as object, 'toString'));
});

test('追加 getter：视图不可见且函数不执行，内容侧访问会执行', () => {
  const { xray, mirror } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;

  assert.equal(view.loot, undefined);
  assert.equal('loot' in view, false);
  assert.equal(Object.keys(view).includes('loot'), false);
  assert.equal(contentGetterCalls, 0);

  const contentView = mirror.mirror('evildoer') as Record<string, unknown>;
  assert.equal(contentView.loot, 'secret');
  assert.equal(contentGetterCalls, 1);

  // 被内容域遮蔽的原生数据属性仍返回原生值。
  assert.equal(view.shadowed, 'native-value');
  assert.equal(view.extra, undefined);
});

test('替换 __proto__：视图原型固定为原生原型，内容原型上的属性不可见', () => {
  const { xray, mirror } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;

  const proto = Object.getPrototypeOf(view) as Record<string, unknown> | null;
  assert.equal(xray.nodeIdOf(proto as object), ID.ObjectPrototype);
  assert.equal(view.__proto__, proto);
  assert.equal(view.stolen, undefined);
  assert.equal(view.probe, undefined);
  assert.equal(fakeProtoProbeCalls, 0);

  // 原生原型方法依然可用，且是特权侧实现。
  assert.equal(typeof (proto as Record<string, unknown>).hasOwnProperty, 'function');
  assert.equal((view.hasOwnProperty as (key: string) => boolean).call(view, 'safeName'), true);
  assert.equal((view.hasOwnProperty as (key: string) => boolean).call(view, 'loot'), false);

  // 对照：内容域模拟侧确实沿替换后的原型读到追加属性。
  const contentView = mirror.mirror('evildoer') as Record<string, unknown>;
  assert.equal(Object.getPrototypeOf(contentView), mirror.mirror('evilProto'));
  assert.equal(contentView.stolen, 'from-evil-proto');
  assert.equal(contentView.probe, 'proto-getter');
  assert.equal(fakeProtoProbeCalls, 1);
});

test('环形引用：视图返回值继续包成视图且身份稳定，展开不死循环', () => {
  const { xray } = setup();
  const leftView = xray.view('left') as Record<string, unknown>;

  const partner = leftView.partner as Record<string, unknown>;
  assert.ok(xray.isView(partner));
  assert.equal(xray.nodeIdOf(partner), 'right');
  assert.equal(partner.name, 'right');

  const back = partner.partner as Record<string, unknown>;
  assert.equal(back, leftView);
  assert.equal(xray.nodeIdOf(back), 'left');

  const snapshot = expandXray(xray, 'left');
  const json = JSON.stringify(snapshot);
  assert.match(json, /"\$cycle":"left"/);
  assert.doesNotThrow(() => expandXray(xray, 'left', { maxDepth: 100 }));

  const shallow = expandXray(xray, 'left', { maxDepth: 1 }) as { own: { partner: unknown } };
  assert.deepEqual(shallow.own.partner, { $depthLimit: 'right' });
});

test('普通原生读取：自有属性、数组槽位与原生方法按 Xray 语义工作', () => {
  const { xray } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;
  assert.equal(view.safeName, 'legit');
  assert.equal(view.nativeOnly, 42);

  const listView = xray.view('list') as unknown as Array<Record<string, unknown>> & {
    length: number;
    join: (separator?: string) => string;
  };
  assert.equal(listView.length, 3);
  assert.equal(listView[0].safeName, 'legit');
  assert.equal(listView[2].name, 'left');
  assert.deepEqual([...listView].slice(1, 2), ['two']);

  // 内容域对下标 0 和 join 的遮蔽都不生效。
  assert.notEqual(listView[0], 'shadowed-slot');
  assert.equal(typeof listView.join, 'function');
  assert.equal(listView.join('-'), '[object Object]-two-[object Object]');

  const keys = Object.keys(listView);
  assert.deepEqual(keys, ['0', '1', '2']);
});

test('访问器属性：只报告存在，不执行任何内容函数', () => {
  const { xray } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;

  const report = view.nativeGetter as { xrayAccessor: boolean; ownerNode: string; key: string };
  assert.deepEqual(report, { xrayAccessor: true, ownerNode: 'evildoer', key: 'nativeGetter' });
  assert.equal(contentGetterCalls, 0);

  const descriptor = xray.describe(view as object, 'nativeGetter');
  assert.ok(descriptor);
  assert.equal(typeof descriptor?.get, 'function');
  assert.equal((descriptor!.get as () => unknown)(), report);
  assert.equal(xray.describe(view as object, 'loot'), undefined);
  assert.deepEqual(xray.contentProperties(view as object).sort(),
    ['extra', 'loot', 'shadowed', 'toString']);
});

test('视图只读：写入、删除、defineProperty 与 preventExtensions 都被拒绝', () => {
  const { xray } = setup();
  const view = xray.view('evildoer') as Record<string, unknown>;

  assert.throws(() => { view.safeName = 'x'; }, /read-only/);
  assert.throws(() => { view.newProp = 1; }, /read-only/);
  assert.throws(() => { delete view.safeName; }, /read-only/);
  assert.throws(() => Object.defineProperty(view, 'safeName', { value: 1 }), /read-only/);
  assert.throws(() => Object.preventExtensions(view), /read-only/);
  assert.equal(view.safeName, 'legit');
});

test('标准运行时工厂与图校验', () => {
  const runtime = createStandardRuntime();
  const protoView = runtime.xray.view(ID.ObjectPrototype) as Record<string, unknown>;
  assert.equal((protoView.toString as () => string).call(protoView), '[object ObjectPrototype]');

  const badGraph = {
    nodes: {
      broken: {
        id: 'broken',
        domain: CONTENT,
        intrinsic: 'Object',
        nativeProto: 'missing',
      },
    },
  };
  assert.throws(() => new Xray(badGraph), /missing node/);

  assert.throws(
    () => standardGraph([{
      id: ID.ObjectPrototype,
      domain: CONTENT,
      intrinsic: 'Object',
    }]),
    /reserved id/,
  );
});
