import assert from 'node:assert/strict';
import test from 'node:test';
import { Compiler } from '../src/dag.ts';
import { PolicyEngine } from '../src/engine.ts';
import { and, inRange, inSet, lit, not, or } from '../src/expr.ts';
import { AttrStore, type Fetcher } from '../src/store.ts';

function countingFetcher(values: Record<string, number | undefined>) {
  const counts = new Map<string, number>();
  const fetcher: Fetcher = async (_subject, attr) => {
    counts.set(attr, (counts.get(attr) ?? 0) + 1);
    return values[attr];
  };
  return { fetcher, counts };
}

test('x OR NOT x 在 x 缺失时仍为 unknown（绝不按 false 化简）', async () => {
  const { fetcher } = countingFetcher({}); // x 缺失
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    or(inSet('x', [1]), not(inSet('x', [1]))),
  );
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.value, 'unknown');
  assert.notEqual(d.value, 'false');
});

test('缺失属性 AND true 仍为 unknown，而不是 false', async () => {
  const { fetcher } = countingFetcher({});
  const engine = new PolicyEngine(new AttrStore(fetcher), and(inSet('x', [1]), lit(true)));
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.value, 'unknown');
});

test('false AND 昂贵属性：编译期折叠，后者不被读取', async () => {
  const { fetcher, counts } = countingFetcher({ expensive: 1 });
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    and(lit(false), inSet('expensive', [1])),
  );
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.value, 'false');
  assert.equal(counts.get('expensive') ?? 0, 0);
});

test('运行期短路：前置条件为 false 时不读取昂贵属性', async () => {
  const { fetcher, counts } = countingFetcher({ cheap: 5, expensive: 1 });
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    and(inRange('cheap', 0, 0), inSet('expensive', [1])),
  );
  const d = await engine.decide('s');
  assert.equal(d.value, 'false');
  assert.equal(counts.get('cheap'), 1);
  assert.equal(counts.get('expensive') ?? 0, 0);
});

test('OR 短路：前置为 true 时不读取后续属性', async () => {
  const { fetcher, counts } = countingFetcher({ a: 1, b: 2 });
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    or(inSet('a', [1]), inSet('b', [2])),
  );
  const d = await engine.decide('s');
  assert.equal(d.value, 'true');
  assert.equal(counts.get('b') ?? 0, 0);
});

test('共享 DAG：相同子表达式哈希cons为同一节点', () => {
  const c = new Compiler();
  const shared = and(inSet('a', [1]), inRange('b', 0, 9));
  const root1 = c.compile(or(shared, not(shared)));
  const root2 = c.compile(or(shared, not(shared)));
  assert.equal(root1, root2); // 结构共享
  assert.equal(root1.kind, 'or');
  if (root1.kind === 'or') {
    const [p, np] = root1.args;
    assert.equal(np.kind, 'not');
    if (np.kind === 'not') assert.equal(np.arg, p); // NOT 共享同一子图
  }
});

test('最小解释切片：只保留决定结论的属性读取', async () => {
  const { fetcher } = countingFetcher({ a: 5, b: 9 });
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    and(inRange('a', 1, 10), inSet('b', [2, 3])),
  );
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.value, 'false');
  // a=5 为 true，与结论无关；b=9 才是 false 的唯一原因
  assert.deepEqual(d.explanation, { b: 9 });
});

test('解释切片尊重短路：未被读取的属性不出现在解释中', async () => {
  const { fetcher } = countingFetcher({ a: 1, b: 2 });
  const engine = new PolicyEngine(
    new AttrStore(fetcher),
    or(inSet('a', [1]), inSet('b', [2])),
  );
  const d = await engine.decide('s');
  assert.equal(d.value, 'true');
  assert.deepEqual(d.explanation, { a: 1 });
});
