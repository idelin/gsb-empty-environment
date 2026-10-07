import { describe, expect, test } from 'bun:test';
import {
  LAYERS,
  SettingsStore,
  deepEqual,
  type Change,
  type JsonObject,
  type JsonValue,
  type LayerName,
} from './settings';

// ---------- 独立的逐层参考模型（与被测实现无关的简单折叠） ----------
type Layers = Record<LayerName, JsonObject>;

function isObj(v: unknown): v is JsonObject {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** 模型合并：对象按叶合并，其余（数组/标量/null）高层整体替换。 */
function modelMerge(low: JsonValue, high: JsonValue): JsonValue {
  if (isObj(low) && isObj(high)) {
    const out: JsonObject = { ...low };
    for (const [k, v] of Object.entries(high)) {
      out[k] = k in out ? modelMerge(out[k], v) : v;
    }
    return out;
  }
  return high;
}

/** 模型解析：从 default -> session 逐层折叠出最终树。 */
function modelResolve(layers: Layers): JsonObject {
  let acc: JsonValue = {};
  for (const layer of LAYERS) acc = modelMerge(acc, layers[layer]);
  return acc as JsonObject;
}

/** 模型来源：逐层收集叶子（非对象值），高层覆盖低层。 */
function modelSources(layers: Layers): Record<string, LayerName> {
  const out: Record<string, LayerName> = {};
  const walk = (v: JsonValue, layer: LayerName, prefix: string[]) => {
    if (isObj(v)) {
      for (const [k, x] of Object.entries(v)) walk(x, layer, [...prefix, k]);
      // 高层对象覆盖低层非对象时，清掉被遮蔽的旧叶子
      const p = prefix.join('.');
      for (const key of Object.keys(out)) {
        if (p !== '' && (key === p || key.startsWith(p + '.'))) {
          // 仅当该叶子不属于本层此子树时删除——简单起见：先删后由 walk 重写
        }
      }
      return;
    }
    out[prefix.join('.')] = layer;
  };
  // 先按层序收集所有叶子，再剔除“被高层非对象遮蔽”的叶子
  const leaves: Array<{ path: string[]; layer: LayerName }> = [];
  const collect = (v: JsonValue, layer: LayerName, prefix: string[]) => {
    if (isObj(v)) {
      for (const [k, x] of Object.entries(v)) collect(x, layer, [...prefix, k]);
    } else {
      leaves.push({ path: prefix, layer });
    }
  };
  for (const layer of LAYERS) collect(layers[layer], layer, []);
  const shadowed = (path: string[], layer: LayerName): boolean => {
    // 若更高层在 path 的某个前缀上放了非对象值，则该叶子被遮蔽
    const idx = LAYERS.indexOf(layer);
    for (let i = idx + 1; i < LAYERS.length; i++) {
      for (let len = 0; len <= path.length; len++) {
        let node: JsonValue = layers[LAYERS[i]];
        let ok = true;
        for (const k of path.slice(0, len)) {
          if (!isObj(node) || !(k in node)) { ok = false; break; }
          node = node[k];
        }
        if (ok && len < path.length && !isObj(node)) return true;
        if (ok && len === path.length && !isObj(node)) return true;
      }
    }
    return false;
  };
  for (const { path, layer } of leaves) {
    if (!shadowed(path, layer)) out[path.join('.')] = layer;
  }
  return out;
}

function valueAt(tree: JsonValue, dotted: string): JsonValue | undefined {
  let node: JsonValue = tree;
  if (dotted === '') return node;
  for (const k of dotted.split('.')) {
    if (!isObj(node)) return undefined;
    node = node[k];
  }
  return node;
}

/** 核心核对：store 的每个叶子值与来源都必须等于逐层模型。 */
function expectMatchesModel(store: SettingsStore) {
  const layers = store.snapshotLayers();
  const tree = modelResolve(layers);
  const sources = modelSources(layers);

  // 整树一致
  expect(deepEqual(store.get(''), tree)).toBe(true);

  // 每个叶子：值一致、来源一致
  for (const [dotted, layer] of Object.entries(sources)) {
    expect(deepEqual(store.get(dotted), valueAt(tree, dotted))).toBe(true);
    const r = store.resolve(dotted);
    expect(r.source).toBe(layer);
    expect(r.sources[dotted]).toBe(layer);
  }

  // 中间对象路径：解析值与模型子树一致
  const checkObjects = (v: JsonValue, prefix: string[]) => {
    if (!isObj(v)) return;
    const got = store.get(prefix.join('.'));
    expect(deepEqual(got, v)).toBe(true);
    for (const [k, x] of Object.entries(v)) checkObjects(x, [...prefix, k]);
  };
  checkObjects(tree, []);
}

// ---------- 行为测试 ----------
describe('layer precedence & null/unset semantics', () => {
  test('高层覆盖低层，source 指向最高定义层', () => {
    const s = new SettingsStore();
    s.set('default', 'a.b', 1);
    s.set('project', 'a.b', 2);
    s.set('session', 'a.b', 3);
    expect(s.get('a.b')).toBe(3);
    expect(s.resolve('a.b').source).toBe('session');
    s.unset('session', 'a.b');
    expect(s.get('a.b')).toBe(2);
    expect(s.resolve('a.b').source).toBe('project');
    s.unset('project', 'a.b');
    expect(s.get('a.b')).toBe(1);
    expect(s.resolve('a.b').source).toBe('default');
    s.unset('default', 'a.b');
    expect(s.get('a.b')).toBeUndefined();
    expect(s.resolve('a.b').source).toBeUndefined();
  });

  test('显式 null 是值，遮蔽下层而不是删除', () => {
    const s = new SettingsStore();
    s.set('default', 'note', 'hello');
    s.set('session', 'note', null);
    expect(s.get('note')).toBeNull();
    expect(s.resolve('note').source).toBe('session');
    // unset 才撤掉当前层，下层浮现
    s.unset('session', 'note');
    expect(s.get('note')).toBe('hello');
    expect(s.resolve('note').source).toBe('default');
  });

  test('unset 不存在的路径是 no-op，不发事件', () => {
    const s = new SettingsStore();
    const events: Change[][] = [];
    s.subscribe((cs) => events.push([...cs]));
    s.unset('session', 'nothing.here');
    expect(events).toHaveLength(0);
  });
});

describe('merge semantics', () => {
  test('对象按叶合并，数组整体替换', () => {
    const s = new SettingsStore();
    s.set('default', 'ui', { theme: 'light', font: { size: 12, family: 'mono' } });
    s.set('project', 'ui.font', { size: 14 });
    s.set('session', 'ui.theme', 'dark');
    expect(s.get('ui')).toEqual({ theme: 'dark', font: { size: 14, family: 'mono' } });
    const r = s.resolve('ui');
    expect(r.source).toBe('merged');
    expect(r.sources['ui.theme']).toBe('session');
    expect(r.sources['ui.font.size']).toBe('project');
    expect(r.sources['ui.font.family']).toBe('default');

    s.set('default', 'list', [1, 2, 3]);
    s.set('project', 'list', [9]);
    expect(s.get('list')).toEqual([9]);
    expect(s.resolve('list').source).toBe('project');
  });

  test('高层标量遮蔽低层对象（整棵子树被替换）', () => {
    const s = new SettingsStore();
    s.set('default', 'extra', { x: 1, y: 2 });
    s.set('session', 'extra', null);
    expect(s.get('extra')).toBeNull();
    expect(s.resolve('extra').source).toBe('session');
    expect(s.get('extra.x')).toBeUndefined();
  });
});

describe('transactions', () => {
  test('多路径更新统一发布一次', () => {
    const s = new SettingsStore();
    const events: Change[][] = [];
    s.subscribe((cs) => events.push([...cs]));
    s.transaction((tx) => {
      tx.set('session', 'a', 1);
      tx.set('project', 'b.c', 2);
      tx.set('default', 'd', [1, 2]);
    });
    expect(events).toHaveLength(1);
    expect(events[0]).toHaveLength(3);
    expect(s.get('a')).toBe(1);
    expect(s.get('b.c')).toBe(2);
    expect(s.get('d')).toEqual([1, 2]);
  });

  test('异常时整体回滚且不发布', () => {
    const s = new SettingsStore();
    s.set('default', 'keep', 'old');
    const events: Change[][] = [];
    s.subscribe((cs) => events.push([...cs]));
    expect(() =>
      s.transaction((tx) => {
        tx.set('default', 'keep', 'new');
        tx.set('session', 'temp', 1);
        throw new Error('boom');
      }),
    ).toThrow('boom');
    expect(s.get('keep')).toBe('old');
    expect(s.get('temp')).toBeUndefined();
    expect(events).toHaveLength(0);
  });

  test('非事务的单次 set 立即发布', () => {
    const s = new SettingsStore();
    const events: Change[][] = [];
    s.subscribe((cs) => events.push([...cs]));
    s.set('session', 'x', 1);
    expect(events).toHaveLength(1);
    expect(events[0][0]).toEqual({ layer: 'session', path: ['x'], kind: 'set' });
  });
});

describe('layered-model cross-check', () => {
  test('复杂场景与逐层模型完全一致', () => {
    const s = new SettingsStore();
    s.set('default', '', {
      ui: { theme: 'light', font: { size: 12, family: 'mono' } },
      list: [1, 2, 3],
      flag: true,
      note: 'd',
    });
    s.set('project', '', { ui: { theme: 'dark' }, list: [9], extra: { x: 1 } });
    s.set('session', '', { ui: { font: { size: 20 } }, note: null, extra: null });
    expectMatchesModel(s);

    // 逐层 unset，每步都与模型核对
    s.unset('session', 'note');
    expectMatchesModel(s);
    s.unset('session', 'extra');
    expectMatchesModel(s);
    s.unset('project', 'list');
    expectMatchesModel(s);
    s.unset('session', 'ui.font');
    expectMatchesModel(s);
    s.unset('project', 'ui');
    expectMatchesModel(s);
  });

  test('事务混合 set/unset 后与模型一致', () => {
    const s = new SettingsStore();
    s.set('default', '', { a: { b: 1, c: [1] }, n: 'x', gone: true });
    s.set('project', '', { a: { b: 2 }, n: null });
    s.transaction((tx) => {
      tx.set('session', 'a.c', [7, 8]);
      tx.unset('project', 'n');
      tx.set('default', 'a.d', { e: null });
      tx.unset('default', 'gone');
      tx.set('project', 'fresh.deep.path', 'v');
    });
    expectMatchesModel(s);
    expect(s.get('n')).toBe('x'); // project 的 null 被 unset 后继承 default
    expect(s.resolve('n').source).toBe('default');
  });
});
