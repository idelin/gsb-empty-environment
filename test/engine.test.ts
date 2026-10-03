import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CepEngine, type CepEvent, type Notification } from '../src/engine.ts';

const PATTERN = {
  name: 'A->B-without-C',
  sequence: ['A', 'B'] as [string, string],
  negation: ['C'],
  windowMs: 10_000,
};

function ev(id: string, type: string, timestamp: number): CepEvent {
  return { id, type, timestamp };
}

function makeEngine() {
  const engine = new CepEngine();
  engine.registerPattern(PATTERN);
  return engine;
}

function matchesOf(ns: Notification[]) {
  return ns.filter((n) => n.kind === 'match');
}

function retractsOf(ns: Notification[]) {
  return ns.filter((n) => n.kind === 'retract');
}

test('验收场景：A@0, B@5 命中；迟到 C@2 精准撤销并记日志', () => {
  const engine = makeEngine();
  const live: Notification[] = [];
  engine.onNotification((n) => live.push(n));

  engine.process(ev('a1', 'A', 0));
  engine.process(ev('b1', 'B', 5_000));

  assert.equal(matchesOf(engine.notifications).length, 1);
  const hit = matchesOf(engine.notifications)[0];
  assert.equal(hit.kind, 'match');
  if (hit.kind === 'match') {
    assert.equal(hit.compensated, false);
    assert.deepEqual(hit.events.map((e) => e.id), ['a1', 'b1']);
  }
  assert.equal(retractsOf(engine.notifications).length, 0);

  // 乱序到达的迟到事件：t=2 的 C
  engine.process(ev('c1', 'C', 2_000));

  assert.equal(retractsOf(engine.notifications).length, 1);
  const retract = retractsOf(engine.notifications)[0];
  if (retract.kind === 'retract') {
    assert.equal(retract.matchId, (hit as { matchId: string }).matchId);
    assert.equal(retract.cause.id, 'c1');
  }
  // 监听器按序收到 match -> retract
  assert.deepEqual(live.map((n) => n.kind), ['match', 'retract']);
  // 日志中同时存在命中与撤销记录
  assert.ok(engine.log.some((l) => l.action === 'match' && l.detail.includes('a1')));
  assert.ok(
    engine.log.some(
      (l) => l.action === 'retract' && l.detail.includes('c1') && l.detail.includes('(0, 5000)'),
    ),
  );
});

test('顺序否定：A@0, C@3, B@5 不产生命中', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('c1', 'C', 3_000));
  engine.process(ev('b1', 'B', 5_000));

  assert.equal(matchesOf(engine.notifications).length, 0);
  assert.ok(engine.log.some((l) => l.action === 'run-killed-by-negation'));
});

test('模式超时：B 超出 10 秒窗口不命中，运行被丢弃并记日志', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('x1', 'X', 20_000)); // 推进水位线，触发超时丢弃
  engine.process(ev('b1', 'B', 21_000));

  assert.equal(matchesOf(engine.notifications).length, 0);
  const timeoutLog = engine.log.find((l) => l.action === 'run-timeout');
  assert.ok(timeoutLog, 'expected a run-timeout log entry');
  assert.match(timeoutLog.detail, /deadline 10000 < watermark 20000/);
});

test('窗口边界：B 恰好落后 A 10 秒仍命中，10.001 秒不命中', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('b1', 'B', 10_000));
  assert.equal(matchesOf(engine.notifications).length, 1);

  const engine2 = makeEngine();
  engine2.process(ev('a1', 'A', 0));
  engine2.process(ev('b1', 'B', 10_001));
  assert.equal(matchesOf(engine2.notifications).length, 0);
});

test('乱序补偿：B@5 先于 A@0 到达，A 到达时补偿触发命中', () => {
  const engine = makeEngine();
  engine.process(ev('b1', 'B', 5_000)); // 先到，无运行可完成
  assert.equal(matchesOf(engine.notifications).length, 0);

  engine.process(ev('a1', 'A', 0)); // 迟到事件 -> 补偿
  const hits = matchesOf(engine.notifications);
  assert.equal(hits.length, 1);
  if (hits[0].kind === 'match') {
    assert.equal(hits[0].compensated, true);
    assert.deepEqual(hits[0].events.map((e) => e.id), ['a1', 'b1']);
  }
  assert.ok(engine.log.some((l) => l.action === 'match-compensated'));
});

test('乱序补偿：迟到的 B 完成已在等待的运行', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('x1', 'X', 8_000)); // 水位线推进到 8s，窗口未过期
  engine.process(ev('b1', 'B', 5_000)); // 迟到的 B

  const hits = matchesOf(engine.notifications);
  assert.equal(hits.length, 1);
  if (hits[0].kind === 'match') assert.equal(hits[0].compensated, true);
});

test('精准撤销：迟到 C 只撤销区间严格包含它的命中', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('a2', 'A', 4_000));
  engine.process(ev('b1', 'B', 5_000)); // 完成 m1(A@0,B@5) 与 m2(A@4,B@5)
  assert.equal(matchesOf(engine.notifications).length, 2);

  engine.process(ev('c1', 'C', 2_000)); // 只落在 (0,5) 内，不在 (4,5) 内
  const retracts = retractsOf(engine.notifications);
  assert.equal(retracts.length, 1);
  if (retracts[0].kind === 'retract') {
    const survivor = matchesOf(engine.notifications).find((m) => m.matchId !== retracts[0].matchId);
    assert.ok(survivor, 'one match must survive');
    if (survivor.kind === 'match') assert.equal(survivor.events[0].id, 'a2');
  }
});

test('窗口外的否定事件不影响命中（C@6 晚于 B@5）', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('b1', 'B', 5_000));
  engine.process(ev('c1', 'C', 6_000));

  assert.equal(matchesOf(engine.notifications).length, 1);
  assert.equal(retractsOf(engine.notifications).length, 0);
});

test('多模式：同一事件流驱动多个独立模式', () => {
  const engine = new CepEngine();
  engine.registerPattern(PATTERN);
  engine.registerPattern({
    name: 'X->Y-without-Z',
    sequence: ['X', 'Y'],
    negation: ['Z'],
    windowMs: 5_000,
  });

  engine.process(ev('a1', 'A', 0));
  engine.process(ev('x1', 'X', 1_000));
  engine.process(ev('b1', 'B', 2_000));
  engine.process(ev('y1', 'Y', 4_000));

  const byPattern = new Map<string, number>();
  for (const n of matchesOf(engine.notifications)) {
    if (n.kind === 'match') byPattern.set(n.pattern, (byPattern.get(n.pattern) ?? 0) + 1);
  }
  assert.equal(byPattern.get('A->B-without-C'), 1);
  assert.equal(byPattern.get('X->Y-without-Z'), 1);
});

test('非确定性：一个 B 可完成多个并发运行（skip-till-any 语义）', () => {
  const engine = makeEngine();
  engine.process(ev('a1', 'A', 0));
  engine.process(ev('a2', 'A', 1_000));
  engine.process(ev('a3', 'A', 2_000));
  engine.process(ev('b1', 'B', 5_000));

  assert.equal(matchesOf(engine.notifications).length, 3);
});
