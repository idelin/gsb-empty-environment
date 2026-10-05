import { diffTraces, DiffReport, StepEvent, TraceEvent } from '../src';

let passed = 0;
let failed = 0;

function check(name: string, cond: boolean, extra?: unknown): void {
  if (cond) {
    passed++;
    console.log(`  ok  ${name}`);
  } else {
    failed++;
    console.error(`FAIL  ${name}`);
    if (extra !== undefined) console.error('      ' + JSON.stringify(extra, null, 2));
  }
}

const step = (action: string, value?: unknown): StepEvent => ({ kind: 'step', action, value });
const loop = (repeat: number, ...body: TraceEvent[]): TraceEvent => ({ kind: 'loop', repeat, body });

const SPEC = { actions: ['http.request', 'db.*'] };

// ---------- 1. 内部步骤数量不同但语义一致 ----------
{
  const a: TraceEvent[] = [
    step('cache.warm'),
    step('http.request', { url: '/u', code: 200 }),
    loop(3, step('metrics.flush'), step('db.write', 1)),
    step('gc'),
  ];
  const b: TraceEvent[] = [
    step('cache.warm'), step('cache.warm2'), step('log.rotate'),
    step('http.request', { url: '/u', code: 200 }),
    loop(3, step('db.write', 1)), // 无 metrics.flush，但它是内部步骤
    step('gc'), step('gc2'),
  ];
  const r = diffTraces(a, b, SPEC);
  check('内部步骤差异被忽略，语义一致', r.equal, r.divergence);
  check('可观察动作计数正确', r.observedCounts.a === 4 && r.observedCounts.b === 4, r.observedCounts);
}

// ---------- 2. 值差异 ----------
{
  const a: TraceEvent[] = [step('init'), step('http.request', 200), step('teardown')];
  const b: TraceEvent[] = [step('init'), step('init2'), step('http.request', 500), step('teardown')];
  const r = diffTraces(a, b, SPEC);
  check('检出值差异', !r.equal && r.divergence!.kind === 'value-mismatch', r.divergence);
  check('值差异位置正确', r.divergence!.observableIndex === 0, r.divergence);
  check('窗口还原分歧前内部步骤', r.window.b.before.some((s) => s.action === 'init2'), r.window);
  check('窗口还原分歧后内部步骤', r.window.a.after.some((s) => s.action === 'teardown'), r.window);
}

// ---------- 3. 缺失动作（一侧耗尽） ----------
{
  const a: TraceEvent[] = [step('http.request', 1), step('db.write', 2)];
  const b: TraceEvent[] = [step('http.request', 1)];
  const r = diffTraces(a, b, SPEC);
  check('检出缺失动作', !r.equal && r.divergence!.kind === 'missing-action', r.divergence);
  check('缺失的是 db.write', r.divergence!.message.includes('db.write'), r.divergence!.message);
}

// ---------- 4. 乱序 ----------
{
  const a: TraceEvent[] = [step('http.request', 'a'), step('db.write', 'b')];
  const b: TraceEvent[] = [step('db.write', 'b'), step('http.request', 'a')];
  const r = diffTraces(a, b, SPEC);
  check('检出乱序', !r.equal && r.divergence!.kind === 'out-of-order', r.divergence);
  check('乱序前瞻命中', r.divergence!.lookahead!.foundAtOffset === 1, r.divergence);
}

// ---------- 5. 循环压缩段：巨大 repeat 不整体展开 ----------
{
  const huge = loop(1_000_000_000, step('tick'), step('db.write', 'x'));
  const a: TraceEvent[] = [huge];
  const b: TraceEvent[] = [loop(1_000_000_000, step('tick'), step('db.write', 'y'))];
  const t0 = Date.now();
  const r = diffTraces(a, b, SPEC);
  const ms = Date.now() - t0;
  check('巨型循环段上检出值差异', !r.equal && r.divergence!.kind === 'value-mismatch', r.divergence);
  check('未整体展开（快速返回）', ms < 500, `${ms}ms`);
  check('路径含循环轮次', r.divergence!.a!.path.includes('第1轮'), r.divergence!.a!.path);
}

// ---------- 6. 嵌套循环中段内分歧 ----------
{
  const a: TraceEvent[] = [
    loop(2, step('outer'), loop(3, step('inner'), step('db.write', 'ok'))),
  ];
  const b: TraceEvent[] = [
    loop(2, step('outer'), loop(3, step('inner'), step('db.write', 'bad'))),
  ];
  const r = diffTraces(a, b, SPEC);
  check('嵌套循环内检出值差异', !r.equal && r.divergence!.kind === 'value-mismatch', r.divergence);
  check('路径反映嵌套循环', /loop.*loop/.test(r.divergence!.a!.path), r.divergence!.a!.path);
}

// ---------- 7. 循环中段内乱序 ----------
{
  const a: TraceEvent[] = [loop(5, step('http.request', 1), step('db.write', 2))];
  const b: TraceEvent[] = [loop(5, step('db.write', 2), step('http.request', 1))];
  const r = diffTraces(a, b, SPEC);
  check('循环体内检出乱序', !r.equal && r.divergence!.kind === 'out-of-order', r.divergence);
}

// ---------- 8. 循环次数不同导致缺失 ----------
{
  const a: TraceEvent[] = [loop(3, step('db.write', 1))];
  const b: TraceEvent[] = [loop(2, step('db.write', 1))];
  const r = diffTraces(a, b, SPEC);
  check('循环次数差异检出缺失', !r.equal && r.divergence!.kind === 'missing-action', r.divergence);
  check('缺失发生在第 2 个可观察动作之后', r.divergence!.observableIndex === 2, r.divergence);
}

// ---------- 9. 通配符可观察定义 ----------
{
  const a: TraceEvent[] = [step('db.read', 1), step('http.request', 2)];
  const b: TraceEvent[] = [step('db.read', 9), step('http.request', 2)];
  const r = diffTraces(a, b, SPEC);
  check('通配符匹配 db.* 检出值差异', !r.equal && r.divergence!.kind === 'value-mismatch', r.divergence);
  const r2 = diffTraces(a, b, { actions: ['http.request'] });
  check('db.read 不在定义内则被忽略', r2.equal, r2.divergence);
}

// ---------- 10. 完全相等 ----------
{
  const a: TraceEvent[] = [step('http.request', 1), loop(2, step('db.write', 'w'))];
  const r = diffTraces(a, a, SPEC);
  check('完全相同的轨迹语义一致', r.equal, r.divergence);
}

// ---------- 11. 空轨迹 ----------
{
  const r = diffTraces([], [], SPEC);
  check('空轨迹一致', r.equal);
  const r2 = diffTraces([step('db.write', 1)], [], SPEC);
  check('对空轨迹检出缺失', !r2.equal && r2.divergence!.kind === 'missing-action', r2.divergence);
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);

// ---------- 演示输出 ----------
const demo: DiffReport = diffTraces(
  [step('span.enter'), loop(3, step('alloc'), step('http.request', { code: 200 }))],
  [step('span.enter'), loop(3, step('alloc'), step('http.request', { code: 500 }))],
  SPEC,
);
console.log('\n演示（循环压缩段内值差异）:');
console.log('  类型:', demo.divergence!.kind);
console.log('  信息:', demo.divergence!.message);
console.log('  A 侧分歧前内部窗口:', demo.window.a.before.map((s) => s.action).join(', '));
