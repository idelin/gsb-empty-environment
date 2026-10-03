import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Doc, type Json } from '../src/index.ts';

/** 构造已同步到同一基础状态的两个副本。 */
function syncedPair(base: Json): { alice: Doc; bob: Doc } {
  const seed = new Doc('seed');
  for (const [key, value] of Object.entries(base as Record<string, Json>)) {
    (seed.root as any)[key] = value;
  }
  return { alice: seed.fork('alice'), bob: seed.fork('bob') };
}

describe('验收边界：并发修改同一列表项的不同层级属性', () => {
  it('root.list[0].title 与 root.list[0].tags.push 三向合并后均生效，且对象引用不被覆盖', () => {
    const { alice, bob } = syncedPair({
      list: [{ title: 'draft', tags: ['js'] }],
    });

    // 合并前各自拿到列表项的内存引用
    const aliceItem = alice.root.list[0];
    const bobItem = bob.root.list[0];

    // 并发变更：alice 改 Register，bob 向嵌套 List 追加
    alice.root.list[0].title = 'published';
    bob.root.list[0].tags.push('go');

    // 双向合并（三向：共同祖先 + 两个并发分支）
    Doc.merge(alice, bob);

    // 两个属性都正确更新，互不覆盖
    assert.equal(alice.root.list[0].title, 'published');
    assert.deepEqual(alice.root.list[0].tags.toArray(), ['js', 'go']);
    assert.equal(bob.root.list[0].title, 'published');
    assert.deepEqual(bob.root.list[0].tags.toArray(), ['js', 'go']);

    // 列表项的内存对象引用未被合并覆盖（就地更新）
    assert.strictEqual(alice.root.list[0], aliceItem);
    assert.strictEqual(bob.root.list[0], bobItem);
    assert.strictEqual(alice.root.list[0].tags, aliceItem.tags);

    // 双方状态完全收敛
    assert.deepEqual(alice.toJSON(), bob.toJSON());
    assert.deepEqual(alice.toJSON(), {
      list: [{ title: 'published', tags: ['js', 'go'] }],
    });
  });
});

describe('混合嵌套结构', () => {
  it('Map / List / Register 任意深度嵌套读写', () => {
    const doc = new Doc('a');
    doc.root.user = {
      name: 'delin',
      age: 30,
      active: true,
      address: { city: 'shanghai', geo: { lat: 31.2, lng: 121.5 } },
      hobbies: ['coding', ['nested', 'list']],
    };
    assert.equal(doc.root.user.address.geo.lat, 31.2);
    assert.deepEqual(doc.root.user.hobbies[1].toArray(), ['nested', 'list']);
    assert.deepEqual(doc.toJSON(), {
      user: {
        name: 'delin',
        age: 30,
        active: true,
        address: { city: 'shanghai', geo: { lat: 31.2, lng: 121.5 } },
        hobbies: ['coding', ['nested', 'list']],
      },
    });
  });

  it('深层并发修改不同键互不干扰', () => {
    const { alice, bob } = syncedPair({ a: { b: { x: 1, y: 2 } } });
    alice.root.a.b.x = 10;
    bob.root.a.b.y = 20;
    Doc.merge(alice, bob);
    assert.deepEqual(alice.toJSON(), { a: { b: { x: 10, y: 20 } } });
    assert.deepEqual(bob.toJSON(), alice.toJSON());
  });
});

describe('合并语义', () => {
  it('合并幂等：重复合并同一日志不产生重复元素', () => {
    const { alice, bob } = syncedPair({ tags: ['js'] });
    bob.root.tags.push('go');
    alice.mergeFrom(bob);
    alice.mergeFrom(bob);
    Doc.merge(alice, bob);
    Doc.merge(alice, bob);
    assert.deepEqual(alice.toJSON(), { tags: ['js', 'go'] });
  });

  it('合并可交换：不同合并顺序收敛到同一状态', () => {
    const { alice, bob } = syncedPair({ n: 0, list: [] });
    alice.root.n = 1;
    alice.root.list.push('from-alice');
    bob.root.n = 2;
    bob.root.list.push('from-bob');

    const a1 = alice.fork('a1');
    a1.mergeFrom(alice);
    a1.mergeFrom(bob);
    const b1 = bob.fork('b1');
    b1.mergeFrom(bob);
    b1.mergeFrom(alice);
    assert.deepEqual(a1.toJSON(), b1.toJSON());
  });

  it('Register 并发写同一键：LWW 确定性收敛', () => {
    const { alice, bob } = syncedPair({ v: 'init' });
    alice.root.v = 'A';
    bob.root.v = 'B';
    Doc.merge(alice, bob);
    assert.equal(alice.toJSON().v, bob.toJSON().v);
    assert.ok(['A', 'B'].includes(alice.toJSON().v as string));
  });

  it('List 并发同位置 push：双方顺序一致（RGA 决胜）', () => {
    const { alice, bob } = syncedPair({ list: ['base'] });
    alice.root.list.push('a1');
    alice.root.list.push('a2');
    bob.root.list.push('b1');
    Doc.merge(alice, bob);
    const la = alice.root.list.toArray();
    const lb = bob.root.list.toArray();
    assert.deepEqual(la, lb);
    assert.deepEqual([...la].sort(), ['a1', 'a2', 'b1', 'base']);
  });

  it('Map 删除与并发 set：操作序大者胜', () => {
    const { alice, bob } = syncedPair({ k: 'v' });
    delete alice.root.k; // alice 删除
    bob.root.k = 'resurrected'; // bob 并发写
    Doc.merge(alice, bob);
    // 两副本收敛到同一结果（由 opId 全序决定，与合并方向无关）
    assert.deepEqual(alice.toJSON(), bob.toJSON());
  });

  it('List 删除：deleteAt 墓碑在合并后生效', () => {
    const { alice, bob } = syncedPair({ list: ['x', 'y', 'z'] });
    alice.root.list.deleteAt(1);
    Doc.merge(alice, bob);
    assert.deepEqual(bob.root.list.toArray(), ['x', 'z']);
    assert.deepEqual(alice.toJSON(), bob.toJSON());
  });

  it('List 下标赋值（替换）与 insertAt', () => {
    const doc = new Doc('a');
    doc.root.list = [1, 2, 3];
    doc.root.list[1] = 20;
    doc.root.list.insertAt(0, 0);
    assert.deepEqual(doc.root.list.toArray(), [0, 1, 20, 3]);
  });

  it('fork 出来的副本与源文档状态一致且日志独立增长', () => {
    const seed = new Doc('seed');
    seed.root.a = { b: [1, 2] };
    const copy = seed.fork('copy');
    assert.deepEqual(copy.toJSON(), seed.toJSON());
    copy.root.a.b.push(3);
    assert.deepEqual(seed.toJSON(), { a: { b: [1, 2] } });
    assert.deepEqual(copy.toJSON(), { a: { b: [1, 2, 3] } });
  });
});
