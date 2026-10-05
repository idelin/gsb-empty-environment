// 手工构造的单元测试

import { planRelease, PlanInputError, MAX_PROJECTS } from '../src/planner';
import type { PlanRequest } from '../src/model';
import { assert, validatePlan, validateCycle } from './helpers';

function chain(n: number): PlanRequest {
  // p0 依赖 p1，p1 依赖 p2，... 形成长度为 n 的依赖链
  const projects = [];
  for (let i = 0; i < n; i++) {
    projects.push({ id: 'p' + i, deps: i + 1 < n ? ['p' + (i + 1)] : [] });
  }
  return { capacity: 1, projects };
}

export function run(): void {
  // 1. 依赖链：容量 1 时被迫逐个发布，顺序与依赖相反
  {
    const req = chain(4);
    const r = planRelease(req);
    assert(r.ok, '链应可行');
    if (r.ok) {
      assert(r.batchCount === 4, '容量 1 的 4 链需要 4 批');
      assert(r.batches[0].projects[0] === 'p3', '链尾先发布');
      assert(r.batches[3].projects[0] === 'p0', '链头最后发布');
    }
    const req3 = chain(4);
    req3.capacity = 4;
    const r3 = planRelease(req3);
    assert(r3.ok && r3.batchCount === 4, '严格语义下容量再大 4 链也需 4 批');
  }

  // 2. 依赖环：a<->b 与 c 依赖 a；容量 2 可行，容量 1 不可行
  {
    const req: PlanRequest = {
      capacity: 2,
      projects: [
        { id: 'a', deps: ['b'] },
        { id: 'b', deps: ['a'] },
        { id: 'c', deps: ['a'] },
      ],
    };
    const r = planRelease(req);
    assert(r.ok, '环大小 2 容量 2 应可行');
    if (r.ok) {
      assert(r.batchCount === 2, '应需 2 批');
      assert(r.batches[0].projects.length === 2, '环同批');
      assert(r.batches[0].reasons.some((x) => x.kind === 'cycle'), '应有 cycle 原因');
    }
    req.capacity = 1;
    const r2 = planRelease(req);
    assert(!r2.ok, '环大小 2 容量 1 应不可行');
    if (!r2.ok) {
      assert(validateCycle(req, r2.witness.cycle), '见证环应真实存在');
      assert(r2.witness.size === 2 && r2.witness.capacity === 1, '见证字段');
    }
  }

  // 3. 贪心会出错的例子：链 a->b->c->d 加 3 个孤立点，容量 2，最优 4 批
  {
    const req: PlanRequest = {
      capacity: 2,
      projects: [
        { id: 'a', deps: ['b'] },
        { id: 'b', deps: ['c'] },
        { id: 'c', deps: ['d'] },
        { id: 'd', deps: [] },
        { id: 'e', deps: [] },
        { id: 'f', deps: [] },
        { id: 'g', deps: [] },
      ],
    };
    const r = planRelease(req);
    assert(r.ok && r.batchCount === 4, '链 4 + 3 孤立点容量 2 最优为 4 批');
    assert(validatePlan(req, r) === null, '计划应有效');
  }

  // 4. 菱形依赖：a 依赖 b,c；b,c 依赖 d
  {
    const req: PlanRequest = {
      capacity: 2,
      projects: [
        { id: 'a', deps: ['b', 'c'] },
        { id: 'b', deps: ['d'] },
        { id: 'c', deps: ['d'] },
        { id: 'd', deps: [] },
      ],
    };
    const r = planRelease(req);
    assert(r.ok && r.batchCount === 3, '菱形容量 2 最优 3 批');
    assert(validatePlan(req, r) === null, '菱形计划应有效');
  }

  // 5. 自依赖不构成约束（自己必然与自己同批）
  {
    const req: PlanRequest = { capacity: 1, projects: [{ id: 'a', deps: ['a'] }] };
    const r = planRelease(req);
    assert(r.ok && r.batchCount === 1, '自环容量 1 可行');
  }

  // 6. 非法输入
  {
    const bad: Array<PlanRequest> = [
      { capacity: 0, projects: [{ id: 'a', deps: [] }] },
      { capacity: 1, projects: [] },
      { capacity: 1, projects: [{ id: 'a', deps: ['ghost'] }] },
      { capacity: 1, projects: [{ id: 'a', deps: [] }, { id: 'a', deps: [] }] },
      {
        capacity: 1,
        projects: Array.from({ length: MAX_PROJECTS + 1 }, (_, i) => ({ id: 'x' + i, deps: [] })),
      },
    ];
    for (const req of bad) {
      let threw = false;
      try {
        planRelease(req);
      } catch (e) {
        threw = e instanceof PlanInputError;
      }
      assert(threw, '非法输入应抛 PlanInputError');
    }
  }

  // 7. 三元环容量 2：见证分量大小 3
  {
    const req: PlanRequest = {
      capacity: 2,
      projects: [
        { id: 'a', deps: ['b'] },
        { id: 'b', deps: ['c'] },
        { id: 'c', deps: ['a'] },
      ],
    };
    const r = planRelease(req);
    assert(!r.ok, '三元环容量 2 不可行');
    if (!r.ok) {
      assert(r.witness.size === 3, '见证分量大小 3');
      assert(r.witness.cycle.length >= 3, '见证环至少 3 个节点');
      assert(validateCycle(req, r.witness.cycle), '见证环有效');
    }
  }

  // 8. 同批原因：容量分组的 capacity 原因
  {
    const req: PlanRequest = {
      capacity: 3,
      projects: [
        { id: 'x', deps: [] },
        { id: 'y', deps: [] },
        { id: 'z', deps: [] },
      ],
    };
    const r = planRelease(req);
    assert(r.ok && r.batchCount === 1, '3 孤立点容量 3 一批');
    if (r.ok) {
      assert(r.batches[0].reasons.some((x) => x.kind === 'capacity'), '应有 capacity 原因');
    }
  }

  console.log('unit: 8 组用例全部通过');
}
