import test from 'node:test';
import assert from 'node:assert/strict';
import { solve, Solver } from '../src/index.ts';
import type { PackageDef, SolveInput } from '../src/index.ts';

function pkg(
  name: string,
  versions: number[],
  per: Record<number, { requires?: PackageDef['versions'][number]['requires']; conflicts?: PackageDef['versions'][number]['conflicts'] }> = {},
): PackageDef {
  return {
    name,
    versions: versions.map((v) => ({ version: v, ...(per[v] ?? {}) })),
  };
}

test('全新安装时选择最高可用版本', () => {
  const res = solve({
    repository: [pkg('a', [1, 2, 3])],
    required: [{ pkg: 'a' }],
  });
  assert.deepEqual(res, { ok: true, plan: { a: 3 }, upgrades: ['a'] });
});

test('依赖的版本区间会约束传播到其它包', () => {
  const res = solve({
    repository: [
      pkg('app', [1], { 1: { requires: [{ pkg: 'lib', range: { min: 2, max: 4 } }] } }),
      pkg('lib', [1, 2, 3, 4, 5]),
    ],
    required: [{ pkg: 'app' }],
  });
  assert.deepEqual(res, { ok: true, plan: { app: 1, lib: 4 }, upgrades: ['app', 'lib'] });
});

test('已安装版本满足约束时不升级（升级数为 0）', () => {
  const res = solve({
    repository: [pkg('a', [1, 2, 3])],
    required: [{ pkg: 'a', range: { min: 1 } }],
    installed: { a: 2 },
  });
  assert.deepEqual(res, { ok: true, plan: { a: 2 }, upgrades: [] });
});

test('只升级被迫升级的包，其余保持现状', () => {
  const res = solve({
    repository: [
      pkg('a', [1, 2], { 2: { requires: [{ pkg: 'b', range: { min: 2 } }] } }),
      pkg('b', [1, 2, 3]),
    ],
    required: [{ pkg: 'a', range: { min: 2 } }],
    installed: { a: 1, b: 1 },
  });
  // a 必须升到 2，b 随之必须升到 >=2；b 在 2/3 间升级数相同，取较高版本 3。
  assert.deepEqual(res, { ok: true, plan: { a: 2, b: 3 }, upgrades: ['a', 'b'] });
});

test('升级数并列时优先更高版本', () => {
  const res = solve({
    repository: [
      pkg('app', [1], { 1: { requires: [{ pkg: 'lib', range: { min: 1, max: 2 } }] } }),
      pkg('lib', [1, 2]),
    ],
    required: [{ pkg: 'app' }],
  });
  assert.deepEqual(res, { ok: true, plan: { app: 1, lib: 2 }, upgrades: ['app', 'lib'] });
});

test('菱形依赖下的版本向量字典序最大', () => {
  const res = solve({
    repository: [
      pkg('app', [1], {
        1: { requires: [{ pkg: 'b' }, { pkg: 'c' }] },
      }),
      pkg('b', [1, 2], {
        1: { requires: [{ pkg: 'd', range: { min: 1, max: 2 } }] },
        2: { requires: [{ pkg: 'd', range: { min: 1, max: 1 } }] },
      }),
      pkg('c', [1, 2], {
        1: { requires: [{ pkg: 'd', range: { min: 2, max: 3 } }] },
        2: { requires: [{ pkg: 'd', range: { min: 1, max: 3 } }] },
      }),
      pkg('d', [1, 2, 3]),
    ],
    required: [{ pkg: 'app' }],
  });
  // 所有方案升级数相同；按包名字典序优先取 b 的更高版本。
  assert.deepEqual(res, { ok: true, plan: { app: 1, b: 2, c: 2, d: 1 }, upgrades: ['app', 'b', 'c', 'd'] });
});

test('可选特性：未启用时不拉入对应依赖', () => {
  const repository = [
    pkg('app', [1], {
      1: { requires: [{ pkg: 'tls', range: { min: 2 }, feature: 'tls' }] },
    }),
    pkg('tls', [1, 2, 3]),
  ];
  const off = solve({ repository, required: [{ pkg: 'app' }] });
  assert.deepEqual(off, { ok: true, plan: { app: 1 }, upgrades: ['app'] });

  const on = solve({ repository, required: [{ pkg: 'app', features: ['tls'] }] });
  assert.deepEqual(on, { ok: true, plan: { app: 1, tls: 3 }, upgrades: ['app', 'tls'] });
});

test('软依赖：不主动拉入，但目标被安装时必须满足区间', () => {
  const repository = [
    pkg('app', [1], {
      1: { requires: [{ pkg: 'logger', range: { min: 2 }, optional: true }] },
    }),
    pkg('logger', [1, 2]),
  ];
  const alone = solve({ repository, required: [{ pkg: 'app' }] });
  assert.deepEqual(alone, { ok: true, plan: { app: 1 }, upgrades: ['app'] });

  const together = solve({ repository, required: [{ pkg: 'app' }, { pkg: 'logger' }] });
  assert.deepEqual(together, { ok: true, plan: { app: 1, logger: 2 }, upgrades: ['app', 'logger'] });
});

test('跨包冲突：自动避开不兼容版本', () => {
  const res = solve({
    repository: [
      pkg('a', [1], { 1: { conflicts: [{ pkg: 'b', range: { min: 2 } }] } }),
      pkg('b', [1, 2]),
    ],
    required: [{ pkg: 'a' }, { pkg: 'b' }],
  });
  assert.deepEqual(res, { ok: true, plan: { a: 1, b: 1 }, upgrades: ['a', 'b'] });
});

test('无解时返回可复核的冲突子集，而非最后失败的依赖', () => {
  const input: SolveInput = {
    repository: [
      pkg('x', [1], { 1: { requires: [{ pkg: 'c', range: { min: 2 } }] } }),
      pkg('y', [1], { 1: { requires: [{ pkg: 'c', range: { max: 1 } }] } }),
      pkg('c', [1, 2]),
    ],
    required: [{ pkg: 'x' }, { pkg: 'y' }],
  };
  const res = solve(input);
  assert.equal(res.ok, false);
  if (res.ok) return;

  // 冲突子集同时包含两个根需求与两条依赖边，而不是只有最后失败的那条。
  assert.equal(res.conflict.constraints.length, 4);
  const kinds = res.conflict.constraints.map((c) => c.kind).sort();
  assert.deepEqual(kinds, ['requires', 'requires', 'root', 'root']);
  const text = res.conflict.summary.join('\n');
  assert.match(text, /要求安装 x/);
  assert.match(text, /要求安装 y/);
  assert.match(text, /x@1 依赖 c@>=2/);
  assert.match(text, /y@1 依赖 c@<=1/);

  // 可复核：子集本身无解，且去掉任意一条即可解。
  assert.equal(res.conflict.verify(), true);
});

test('根需求之间直接冲突时的冲突子集', () => {
  const res = solve({
    repository: [pkg('a', [1, 2, 3])],
    required: [{ pkg: 'a', range: { min: 3 } }, { pkg: 'a', range: { max: 1 } }],
  });
  assert.equal(res.ok, false);
  if (res.ok) return;
  assert.equal(res.conflict.constraints.length, 2);
  assert.equal(res.conflict.verify(), true);
});

test('依赖的包在仓库中不存在时，冲突子集指向对应根需求', () => {
  const res = solve({
    repository: [pkg('app', [1], { 1: { requires: [{ pkg: 'ghost' }] } })],
    required: [{ pkg: 'app' }],
  });
  assert.equal(res.ok, false);
  if (res.ok) return;
  assert.equal(res.conflict.verify(), true);
  const text = res.conflict.summary.join('\n');
  assert.match(text, /app@1 依赖 ghost/);
});

test('二十个包的长依赖链：可解且只升级必要的包', () => {
  const repository: PackageDef[] = [];
  const installed: Record<string, number> = {};
  for (let i = 0; i < 20; i++) {
    const name = `p${i}`;
    installed[name] = 1;
    repository.push(
      pkg(name, [1, 2, 3], i < 19
        ? Object.fromEntries([1, 2, 3].map((v) => [v, { requires: [{ pkg: `p${i + 1}`, range: { min: 1, max: 3 } }] }]))
        : {}),
    );
  }
  const res = solve({
    repository,
    required: [{ pkg: 'p0', range: { min: 3, max: 3 } }],
    installed,
  });
  assert.equal(res.ok, true);
  if (!res.ok) return;
  assert.equal(Object.keys(res.plan).length, 20);
  assert.equal(res.plan.p0, 3);
  assert.deepEqual(res.upgrades, ['p0']);
});

test('二十个包的成对冲突：无解时冲突子集仍然极小', () => {
  // web 的每个版本都与 sec 的所有版本冲突，而两者都被根需求要求安装。
  const repository: PackageDef[] = [
    pkg('web', [1, 2, 3], Object.fromEntries([1, 2, 3].map((v) => [v, { conflicts: [{ pkg: 'sec' }] }]))),
    pkg('sec', [1, 2]),
    // 填充若干无关包，模拟 20 包规模。
    ...Array.from({ length: 17 }, (_, i) => pkg(`filler${i}`, [1])),
  ];
  const res = solve({ repository, required: [{ pkg: 'web' }, { pkg: 'sec' }] });
  assert.equal(res.ok, false);
  if (res.ok) return;
  // 极小子集：两个根需求 + web 三个版本各自的冲突边。
  assert.equal(res.conflict.constraints.length, 5);
  assert.equal(res.conflict.verify(), true);
});

test('非整数版本会被拒绝', () => {
  assert.throws(
    () => new Solver({ repository: [pkg('a', [1.5])], required: [{ pkg: 'a' }] }),
    /非负整数/,
  );
});

test('区间下界大于上界会被拒绝', () => {
  assert.throws(
    () => new Solver({ repository: [pkg('a', [1])], required: [{ pkg: 'a', range: { min: 3, max: 1 } }] }),
    /下界大于上界/,
  );
});
