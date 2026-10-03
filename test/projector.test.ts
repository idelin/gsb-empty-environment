import { describe, it } from './harness';
import { flatten, project, projectValue } from '../src/index';
import { buildOrder, buildOrderJson } from './fixture';

declare const require: (id: string) => any;
const assert = require('node:assert/strict');

describe('flatten: 自动推断字段路径并抽取虚拟列', () => {
  it('推断嵌套订单的全部叶子路径', () => {
    const { store } = flatten(buildOrderJson());
    const paths = store.paths();
    assert.ok(paths.includes('user.address.city'));
    assert.ok(paths.includes('user.address.geo.lng'));
    assert.ok(paths.includes('items[].sku'));
    assert.ok(paths.includes('items[].attrs.color'));
    assert.ok(paths.includes('payment.vouchers[].code'));
    assert.ok(paths.includes('tags[]'));
    assert.ok(paths.includes('ext'));
  });

  it("按 'user.address.city' 建立独立列存切片", () => {
    const { store } = flatten(buildOrderJson());
    assert.deepStrictEqual(store.getColumn('user.address.city'), ['上海市']);
    assert.deepStrictEqual(store.getColumn('items[].sku'), [['SKU-1001', 'SKU-2002']]);
    assert.deepStrictEqual(store.getColumn('tags[]'), [['加急', '礼品包装']]);
    assert.deepStrictEqual(store.getColumn('ext'), [null]);
  });
});

describe('project: 按需投影，只物化相关虚拟列', () => {
  it('仅请求城市维度时只产生对应列', () => {
    const { store } = project(buildOrderJson(), ['user.address.city']);
    assert.deepStrictEqual(store.paths(), ['user.address.city']);
    assert.deepStrictEqual(store.getColumn('user.address.city'), ['上海市']);
  });

  it('无关结构（商品列表等）被整体跳过，不做反序列化', () => {
    const json = buildOrderJson();
    const { store, stats } = project(json, ['user.address.city']);
    assert.ok(stats.skippedSubtrees > 0, '应当存在被跳过的子树');
    assert.ok(
      stats.skippedBytes > stats.totalBytes * 0.5,
      `跳过字节应超过总量一半: skipped=${stats.skippedBytes} total=${stats.totalBytes}`,
    );
    assert.ok(!store.has('items[].sku'));
    assert.ok(!store.has('payment.total'));
  });

  it('投影过程不调用 JSON.parse（无整文档反序列化）', () => {
    const original = JSON.parse;
    let calls = 0;
    JSON.parse = (() => {
      calls++;
      throw new Error('JSON.parse should not be called for leaf projection');
    }) as typeof JSON.parse;
    try {
      const { store } = project(buildOrderJson(), [
        'orderId',
        'user.address.city',
        'items[].sku',
        'items[0].price',
        'tags[]',
      ]);
      assert.strictEqual(calls, 0);
      assert.deepStrictEqual(store.getColumn('orderId'), ['SO-20261003-0001']);
      assert.deepStrictEqual(store.getColumn('user.address.city'), ['上海市']);
      assert.deepStrictEqual(store.getColumn('items[].sku'), [['SKU-1001', 'SKU-2002']]);
      assert.deepStrictEqual(store.getColumn('items[0].price'), [399.0]);
      assert.deepStrictEqual(store.getColumn('tags[]'), [['加急', '礼品包装']]);
    } finally {
      JSON.parse = original;
    }
  });

  it('投影结果与全量 JSON.parse 引用一致', () => {
    const json = buildOrderJson();
    const paths = [
      'orderId',
      'user.address.city',
      'user.contacts[].value',
      'items[].sku',
      'items[1].attrs.size',
      'payment.total',
      'logistics.routes[].node',
      'tags[]',
      'ext',
    ];
    const fromText = project(json, paths).store;
    const fromValue = projectValue(JSON.parse(json), paths);
    assert.deepStrictEqual(fromText.paths(), fromValue.paths());
    for (const p of paths) {
      assert.deepStrictEqual(fromText.getColumn(p), fromValue.getColumn(p), `column ${p}`);
    }
  });

  it('请求子树路径时物化该子树（仅对命中子串调用 JSON.parse）', () => {
    const { store } = project(buildOrderJson(), ['user.address']);
    const order = buildOrder();
    assert.deepStrictEqual(store.getColumn('user.address'), [order.user && (order.user as any).address]);
  });

  it('根数组：多记录按行对齐，缺失路径补 undefined', () => {
    const orders = [
      { id: 1, user: { address: { city: '北京' } } },
      { id: 2, user: { name: '无地址' } },
      { id: 3, user: { address: { city: '广州' } } },
    ];
    const { store } = project(JSON.stringify(orders), ['id', 'user.address.city']);
    assert.strictEqual(store.rowCount, 3);
    assert.deepStrictEqual(store.getColumn('id'), [1, 2, 3]);
    assert.deepStrictEqual(store.getColumn('user.address.city'), ['北京', undefined, '广州']);
  });

  it('跳过含引号/括号/转义的无关子树不误判边界', () => {
    const json = JSON.stringify({
      noise: '}{]["\\ 世\n\t',
      nested: { trick: '","},{"' },
      target: { city: '杭州市' },
    });
    const { store } = project(json, ['target.city']);
    assert.deepStrictEqual(store.getColumn('target.city'), ['杭州市']);
    assert.deepStrictEqual(store.paths(), ['target.city']);
  });

  it('非法 JSON 抛出带偏移量的语法错误', () => {
    assert.throws(() => project('{"a": 1,}', ['a']), /at offset/);
    assert.throws(() => flatten('{"a": '), /at offset/);
  });
});
