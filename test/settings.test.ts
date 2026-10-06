import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LAYERS,
  SettingsStore,
  type JsonObject,
  type JsonValue,
  type LayerName,
  type Mutation,
} from '../src/settings.ts';

// ---------- 独立参考模型：逐层原始树 + 扁平化叶子合并（与库实现路径不同） ----------

type Model = Record<LayerName, JsonObject>;

function isObj(v: unknown): v is JsonObject {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function modelSet(model: Model, layer: LayerName, segments: string[], value: JsonValue): void {
  let node = model[layer];
  for (const seg of segments.slice(0, -1)) {
    if (!isObj(node[seg])) node[seg] = {};
    node = node[seg] as JsonObject;
  }
  node[segments[segments.length - 1]] = structuredClone(value);
}

function modelUnset(model: Model, layer: LayerName, segments: string[]): void {
  const stack: Array<[JsonObject, string]> = [];
  let node: JsonValue = model[layer];
  for (const seg of segments) {
    if (!isObj(node)) return;
    stack.push([node, seg]);
    node = node[seg];
  }
  const [parent, key] = stack[stack.length - 1];
  delete parent[key];
  for (let i = stack.length - 2; i >= 0; i -= 1) {
    const [p, k] = stack[i];
    if (isObj(p[k]) && Object.keys(p[k]).length === 0) delete p[k];
  }
}

const SEP = '::';

/** 把一层树拍平成 叶子路径 -> 叶子值（null、数组都是叶子）。 */
function flatten(tree: JsonObject): Map<string, JsonValue> {
  const out = new Map<string, JsonValue>();
  const walk = (node: JsonValue, path: string[]): void => {
    if (isObj(node)) {
      for (const key of Object.keys(node)) walk(node[key], [...path, key]);
    } else {
      out.set(path.join(SEP), node);
    }
  };
  walk(tree, []);
  return out;
}

function isPrefix(a: string[], b: string[]): boolean {
  return a.length <= b.length && a.every((seg, i) => seg === b[i]);
}

/** 高层扁平表覆盖低层：任一方向存在前缀关系即冲突，高层赢。 */
function mergeFlat(low: Map<string, JsonValue>, high: Map<string, JsonValue>): Map<string, JsonValue> {
  const result = new Map(low);
  for (const [highKey, highValue] of high) {
    const highPath = highKey.split(SEP);
    for (const existing of [...result.keys()]) {
      const existingPath = existing.split(SEP);
      if (isPrefix(highPath, existingPath) || isPrefix(existingPath, highPath)) result.delete(existing);
    }
    result.set(highKey, highValue);
  }
  return result;
}

function unflatten(flat: Map<string, JsonValue>): JsonObject {
  const root: JsonObject = {};
  for (const [key, value] of flat) {
    const segments = key.split(SEP);
    let node = root;
    for (const seg of segments.slice(0, -1)) {
      if (!isObj(node[seg])) node[seg] = {};
      node = node[seg] as JsonObject;
    }
    node[segments[segments.length - 1]] = value;
  }
  return root;
}

/** 模型解析：返回合并树 + 每个叶子路径的来源层。 */
function modelResolve(model: Model): { tree: JsonObject; provenance: Map<string, LayerName> } {
  const flats = LAYERS.map((layer) => flatten(model[layer]));
  const merged = flats.reduce(mergeFlat, new Map<string, JsonValue>());
  const provenance = new Map<string, LayerName>();
  for (const key of merged.keys()) {
    for (let i = LAYERS.length - 1; i >= 0; i -= 1) {
      if (flats[i].has(key)) {
        provenance.set(key, LAYERS[i]);
        break;
      }
    }
  }
  return { tree: unflatten(merged), provenance };
}

// ---------- 确定性随机 ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- 单元测试：语义逐条锁定 ----------

test('层优先级：session > project > default', () => {
  const store = new SettingsStore({ default: { theme: 'light' } });
  assert.equal(store.get('theme'), 'light');
  assert.equal(store.sourceOf('theme'), 'default');
  store.set('project', 'theme', 'dark');
  assert.equal(store.get('theme'), 'dark');
  assert.equal(store.sourceOf('theme'), 'project');
  store.set('session', 'theme', 'solarized');
  assert.equal(store.get('theme'), 'solarized');
  assert.equal(store.sourceOf('theme'), 'session');
});

test('显式 null 是值：遮蔽下层，来源可追踪', () => {
  const store = new SettingsStore({ default: { proxy: 'http://corp:8080' } });
  store.set('session', 'proxy', null);
  assert.equal(store.get('proxy'), null);
  assert.equal(store.sourceOf('proxy'), 'session');
  const res = store.resolve('proxy');
  assert.deepEqual(res.leaves, [{ path: ['proxy'], value: null, source: 'session' }]);
});

test('unset 只撤当前层，从下层继承', () => {
  const store = new SettingsStore({ default: { fontSize: 12 } });
  store.set('project', 'fontSize', 14);
  store.set('session', 'fontSize', 16);
  store.unset('session', 'fontSize');
  assert.equal(store.get('fontSize'), 14);
  assert.equal(store.sourceOf('fontSize'), 'project');
  store.unset('project', 'fontSize');
  assert.equal(store.get('fontSize'), 12);
  store.unset('default', 'fontSize');
  assert.equal(store.get('fontSize'), undefined);
  assert.equal(store.sourceOf('fontSize'), undefined);
});

test('对象按叶合并，来源逐叶可追踪', () => {
  const store = new SettingsStore({
    default: { editor: { fontSize: 12, tabSize: 4, wordWrap: false } },
  });
  store.set('project', 'editor', { tabSize: 2 });
  store.set('session', 'editor.wordWrap', true);
  assert.deepEqual(store.get('editor'), { fontSize: 12, tabSize: 2, wordWrap: true });
  const res = store.resolve('editor');
  assert.equal(res.source, undefined); // 多层合并，无唯一来源
  const byPath = new Map(res.leaves.map((leaf) => [leaf.path.join('.'), leaf.source]));
  assert.equal(byPath.get('editor.fontSize'), 'default');
  assert.equal(byPath.get('editor.tabSize'), 'project');
  assert.equal(byPath.get('editor.wordWrap'), 'session');
});

test('数组是叶子：整体替换，不逐元素合并', () => {
  const store = new SettingsStore({ default: { include: ['src', 'test'], exclude: ['dist'] } });
  store.set('session', 'include', ['lib']);
  assert.deepEqual(store.get('include'), ['lib']);
  assert.deepEqual(store.get('exclude'), ['dist']);
  assert.equal(store.sourceOf('include'), 'session');
});

test('叶子遮蔽下层对象；上层对象遮蔽下层叶子', () => {
  const store = new SettingsStore({ default: { server: { host: 'a', port: 80 } } });
  store.set('project', 'server', null); // 叶子遮蔽对象
  assert.equal(store.get('server'), null);
  store.set('session', 'server', { port: 8080 }); // 上层对象遮蔽下层叶子
  assert.deepEqual(store.get('server'), { port: 8080 });
  assert.equal(store.sourceOf('server'), 'session');
});

test('事务：多路径统一发布一次，订阅者只看到批次后的状态', () => {
  const store = new SettingsStore({ default: { a: 1, b: 2, c: 3 } });
  const events: Array<{ changes: number; snapshot: JsonValue | undefined }> = [];
  store.subscribe((event) => events.push({ changes: event.changes.length, snapshot: event.resolution.value }));
  store.update([
    { layer: 'session', path: 'a', op: 'set', value: 10 },
    { layer: 'project', path: 'b', op: 'set', value: 20 },
    { layer: 'default', path: 'c', op: 'unset' },
  ]);
  assert.equal(events.length, 1); // 三条路径变更只发布一次
  assert.equal(events[0].changes, 3);
  assert.deepEqual(events[0].snapshot, { a: 10, b: 20 }); // 无中间态
});

test('transaction 回调形式：一次发布，含全部变更', () => {
  const store = new SettingsStore({ default: { x: { p: 1 }, y: 2 } });
  let calls = 0;
  let last: JsonValue | undefined;
  store.subscribe((event) => {
    calls += 1;
    last = event.resolution.value;
  });
  store.transaction((tx) => {
    tx.set('project', 'x.p', 100);
    tx.set('session', 'y', null);
    tx.unset('default', 'x.p');
  });
  assert.equal(calls, 1);
  assert.deepEqual(last, { x: { p: 100 }, y: null });
});

test('unset 后层内空对象被清理', () => {
  const store = new SettingsStore();
  store.set('session', 'a.b.c', 1);
  store.unset('session', 'a.b.c');
  assert.deepEqual(store.layerSnapshot().session, {});
});

test('返回值是深拷贝：外部修改不污染库', () => {
  const store = new SettingsStore({ default: { list: [1, 2], obj: { k: 'v' } } });
  const list = store.get('list') as JsonValue[];
  list.push(3);
  const obj = store.get('obj') as JsonObject;
  obj.k = 'hacked';
  assert.deepEqual(store.get('list'), [1, 2]);
  assert.deepEqual(store.get('obj'), { k: 'v' });
});

test('非法输入被拒绝：undefined 值、__proto__ 键、NaN', () => {
  const store = new SettingsStore();
  assert.throws(() => store.set('session', 'a', undefined as never), /不支持的值类型/);
  assert.throws(() => store.set('session', '__proto__.x', 1), /非法键/);
  assert.throws(() => store.set('session', 'a', JSON.parse('{"__proto__":{}}')), /非法键/);
  assert.throws(() => store.set('session', 'a', NaN), /NaN/);
  assert.equal(store.get('a'), undefined);
});

test('回归：中间层叶子遮蔽后，下层子树不得在高层层对象下重现', () => {
  const store = new SettingsStore({ default: { a: { b: 1 } } });
  store.set('project', 'a', 5); // 叶子遮蔽 default 的整棵 a 子树
  store.set('session', 'a', { c: 2 }); // 上层对象只贡献自己的叶子
  assert.deepEqual(store.get('a'), { c: 2 });
  assert.equal(store.get('a.b'), undefined); // default 的 a.b 已被 project 的叶子永久遮蔽
  assert.deepEqual(store.get('a.c'), 2);
});

test('回归：空库 / 全空对象层解析不崩溃', () => {
  const store = new SettingsStore();
  assert.deepEqual(store.get(), {}); // 根路径永远解析为（可能为空的）合并树
  assert.deepEqual(store.resolve([]), { path: [], value: {}, source: undefined, leaves: [] });
  assert.equal(store.get('missing'), undefined); // 未定义路径仍是 undefined
  store.set('default', 'x', {});
  assert.deepEqual(store.get('x'), {});
  assert.equal(store.sourceOf('x'), undefined); // 空对象无叶子，无来源
});

// ---------- 随机操作对照逐层模型 ----------

test('随机操作序列：库解析结果与逐层参考模型一致', () => {
  const rand = mulberry32(20261006);
  const pick = <T>(items: readonly T[]): T => items[Math.floor(rand() * items.length)];
  const KEYS = ['a', 'b', 'c', 'd'];
  const randomPath = (): string[] => {
    const depth = 1 + Math.floor(rand() * 3);
    return Array.from({ length: depth }, () => pick(KEYS));
  };
  const randomValue = (): JsonValue => {
    switch (Math.floor(rand() * 6)) {
      case 0: return null;
      case 1: return rand() < 0.5;
      case 2: return Math.floor(rand() * 1000);
      case 3: return `s${Math.floor(rand() * 100)}`;
      case 4: return Array.from({ length: Math.floor(rand() * 3) }, () => Math.floor(rand() * 10));
      default: return { [pick(KEYS)]: Math.floor(rand() * 100), [pick(KEYS)]: null };
    }
  };

  for (let round = 0; round < 30; round += 1) {
    const store = new SettingsStore();
    const model: Model = { default: {}, project: {}, session: {} };
    for (let step = 0; step < 200; step += 1) {
      const size = 1 + Math.floor(rand() * 4); // 事务批量 1~4 条
      const batch: Mutation[] = [];
      for (let n = 0; n < size; n += 1) {
        const layer = pick(LAYERS);
        const path = randomPath();
        if (rand() < 0.3) {
          batch.push({ layer, path, op: 'unset' });
        } else {
          batch.push({ layer, path, op: 'set', value: randomValue() });
        }
      }
      store.update(batch);
      for (const mutation of batch) {
        const segments = mutation.path as string[];
        if (mutation.op === 'set') modelSet(model, mutation.layer, segments, mutation.value);
        else modelUnset(model, mutation.layer, segments);
      }
      // 每次事务后整体核对：最终值树
      const expected = modelResolve(model);
      const actual = store.resolve([]);
      assert.deepEqual(actual.value ?? {}, expected.tree);
      // 逐叶来源核对
      const actualProv = new Map(actual.leaves.map((leaf) => [leaf.path.join(SEP), leaf.source]));
      assert.deepEqual(actualProv, expected.provenance);
      // 随机抽 5 条路径做单点 get 核对
      for (let q = 0; q < 5; q += 1) {
        const path = randomPath();
        let expectedNode: JsonValue | undefined = expected.tree;
        for (const seg of path) {
          if (!isObj(expectedNode)) { expectedNode = undefined; break; }
          expectedNode = expectedNode[seg];
        }
        assert.deepEqual(store.get(path), expectedNode === undefined ? undefined : structuredClone(expectedNode));
      }
    }
  }
});
