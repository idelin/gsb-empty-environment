import assert from 'node:assert';
import { ChainEvent, WaitlistEngine } from '../src/waitlist';

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function buildDemo(): WaitlistEngine {
  const e = new WaitlistEngine();
  for (const id of ['A', 'B', 'C']) e.addSession(id, 1, 'G');
  e.register('张三', 'A', { priority: 100, preference: 1 });
  e.register('张三', 'B', { priority: 100, preference: 2 });
  e.register('张三', 'C', { priority: 100, preference: 3 });
  e.register('李四', 'A', { priority: 90, preference: 1 });
  e.register('李四', 'B', { priority: 90, preference: 2 });
  e.register('王五', 'B', { priority: 80, preference: 1 });
  e.register('王五', 'C', { priority: 80, preference: 2 });
  e.register('赵六', 'C', { priority: 70, preference: 1 });
  return e;
}

test('候补队列按优先级降序、报名序号升序排队', () => {
  const e = new WaitlistEngine();
  e.addSession('S', 1);
  e.register('甲', 'S', { priority: 5, preference: 1 }); // 占唯一名额
  e.register('乙', 'S', { priority: 5, preference: 1 });
  e.register('丙', 'S', { priority: 7, preference: 1 });
  e.register('丁', 'S', { priority: 1, preference: 1 });
  e.register('戊', 'S', { priority: 5, preference: 1 });
  assert.deepStrictEqual(e.getConfirmed('S'), ['甲']);
  // 优先级大者排前；同优先级按报名序号（先报先排）
  assert.deepStrictEqual(e.getQueue('S').map((q) => q.personId), ['丙', '乙', '戊', '丁']);
  e.cancel('甲', 'S');
  assert.deepStrictEqual(e.getConfirmed('S'), ['丙']);
  assert.deepStrictEqual(e.getQueue('S').map((q) => q.personId), ['乙', '戊', '丁']);
});

test('互斥组内一人至多一个确认，且只占最偏好的', () => {
  const e = new WaitlistEngine();
  e.addSession('A', 1, 'G');
  e.addSession('B', 1, 'G');
  e.register('张三', 'A', { priority: 1, preference: 1 });
  e.register('张三', 'B', { priority: 1, preference: 2 });
  assert.deepStrictEqual(e.getConfirmed('A'), ['张三']);
  assert.deepStrictEqual(e.getConfirmed('B'), []);
  assert.strictEqual(e.getQueue('B')[0].personId, '张三');
});

test('只接受比当前更偏好的场次：队首也不递补较不偏好的', () => {
  const e = buildDemo();
  // 张三确认 A(偏好1)，虽排在 B 队首(优先级100)，B 有空位也不递补给张三
  assert.deepStrictEqual(e.getConfirmed('A'), ['张三']);
  assert.deepStrictEqual(e.getConfirmed('B'), ['李四']);
  assert.strictEqual(e.getQueue('B')[0].personId, '张三');
});

test('级联递补直到稳定，并输出完整递补链（含因果链接）', () => {
  const e = buildDemo();
  const since = e.lastEventId();
  e.cancel('张三', 'A');
  const events = e.eventsSince(since);
  const kinds = events.map((ev) => ev.kind);
  assert.deepStrictEqual(kinds, ['cancelled', 'promoted', 'released', 'promoted']);

  const [cancelled, liPromoted, liReleased, zhangPromoted] = events as ChainEvent[];
  assert.strictEqual(liPromoted.personId, '李四');
  assert.strictEqual(liPromoted.sessionId, 'A');
  assert.strictEqual(liPromoted.fromSessionId, 'B');
  assert.strictEqual(liPromoted.causedBy, cancelled.id);
  assert.strictEqual(liReleased.sessionId, 'B');
  assert.strictEqual(liReleased.causedBy, liPromoted.id);
  assert.strictEqual(zhangPromoted.personId, '张三');
  assert.strictEqual(zhangPromoted.sessionId, 'B');
  assert.strictEqual(zhangPromoted.causedBy, liReleased.id);

  assert.deepStrictEqual(e.getConfirmed('A'), ['李四']);
  assert.deepStrictEqual(e.getConfirmed('B'), ['张三']);
  assert.deepStrictEqual(e.getConfirmed('C'), ['王五']);
});

test('撤销确认按原候补资格恢复（原位、原序号），而非重新排尾', () => {
  const e = buildDemo();
  e.cancel('张三', 'A'); // 李四递补 A
  const liSeq = e.getQueue('B').find((q) => q.personId === '李四')!.seq;

  e.revokeConfirmation('李四', 'A');
  // 回到 A 候补第 1 位，序号不变，且不会被刚释放的空位立即递补回去
  const queueA = e.getQueue('A');
  assert.strictEqual(queueA[0].personId, '李四');
  assert.strictEqual(queueA[0].declinedOffer, true);
  assert.deepStrictEqual(e.getConfirmed('A'), []);
  assert.strictEqual(liSeq, 5); // 李四是第 5 个报名（B 场），序号终身不变

  // 孙七补位 A；钱八与李四同优先级但报名更晚 → 排在李四原序号之后
  e.register('孙七', 'A', { priority: 10, preference: 1 });
  e.register('钱八', 'A', { priority: 90, preference: 1 });
  assert.deepStrictEqual(e.getConfirmed('A'), ['孙七']);
  assert.deepStrictEqual(e.getQueue('A').map((q) => q.personId), ['李四', '钱八']);

  // 恢复资格后，孙七取消 → 李四凭原候补资格优先递补，而非钱八
  e.reinstate('李四', 'A');
  e.cancel('孙七', 'A');
  assert.deepStrictEqual(e.getConfirmed('A'), ['李四']);
});

test('级联中释放的场次也按原候补位置恢复', () => {
  const e = buildDemo();
  e.cancel('张三', 'A'); // 李四 A←B，李四的 B 回到原候补位置
  e.register('钱九', 'B', { priority: 90, preference: 1 }); // 与李四同优先级但序号更晚
  const queueB = e.getQueue('B');
  // 李四(优先级90/序号5) 回到原位，先于同优先级但序号更晚的钱九
  assert.deepStrictEqual(queueB.map((q) => q.personId), ['李四', '钱九', '王五']);
  assert.strictEqual(queueB[0].personId, '李四');
  assert.ok(queueB.find((q) => q.personId === '李四')!.seq < queueB.find((q) => q.personId === '钱九')!.seq);
});

test('取消后再报名才重新排尾（与撤销恢复形成对比）', () => {
  const e = new WaitlistEngine();
  e.addSession('A', 1);
  e.register('甲', 'A', { priority: 5, preference: 1 });
  e.register('乙', 'A', { priority: 5, preference: 1 });
  e.cancel('乙', 'A');
  e.register('丙', 'A', { priority: 5, preference: 1 });
  e.register('乙', 'A', { priority: 5, preference: 1 }); // 新序号 → 排尾
  assert.deepStrictEqual(e.getQueue('A').map((q) => q.personId), ['丙', '乙']);
});

test('无空位时状态不变，偏好严格改进保证收敛不循环', () => {
  const e = new WaitlistEngine();
  e.addSession('A', 1, 'G');
  e.addSession('B', 1, 'G');
  e.register('P1', 'A', { priority: 9, preference: 1 });
  e.register('P1', 'B', { priority: 9, preference: 2 });
  e.register('P2', 'B', { priority: 8, preference: 1 });
  e.register('P2', 'A', { priority: 8, preference: 2 });
  // P1 确认 A、P2 确认 B，双方都在对方场次候补但都不会接受更差场次
  const before = e.lastEventId();
  e.register('P3', 'A', { priority: 1, preference: 1 }); // 触发 stabilize，无空位
  assert.deepStrictEqual(e.getConfirmed('A'), ['P1']);
  assert.deepStrictEqual(e.getConfirmed('B'), ['P2']);
  const after = e.eventsSince(before).filter((ev) => ev.kind === 'promoted');
  assert.strictEqual(after.length, 0);
});

test('撤销后其他人递补，撤销者仍保留原队列位置', () => {
  const e = new WaitlistEngine();
  e.addSession('A', 1);
  e.register('甲', 'A', { priority: 9, preference: 1 });
  e.register('乙', 'A', { priority: 8, preference: 1 });
  e.register('丙', 'A', { priority: 7, preference: 1 });
  e.revokeConfirmation('甲', 'A');
  // 空位移交给乙；甲仍在候补第 1 位（原资格），不会被自动递补
  assert.deepStrictEqual(e.getConfirmed('A'), ['乙']);
  assert.deepStrictEqual(e.getQueue('A').map((q) => q.personId), ['甲', '丙']);
  // 甲恢复资格后，乙再取消 → 甲凭原位递补
  e.reinstate('甲', 'A');
  e.cancel('乙', 'A');
  assert.deepStrictEqual(e.getConfirmed('A'), ['甲']);
});

test('非法操作抛错', () => {
  const e = new WaitlistEngine();
  e.addSession('A', 1);
  e.register('甲', 'A', { preference: 1 });
  assert.throws(() => e.register('甲', 'A', { preference: 1 }), /重复报名/);
  assert.throws(() => e.register('甲', 'X', { preference: 1 }), /场次不存在/);
  assert.throws(() => e.revokeConfirmation('乙', 'A'), /报名不存在/);
  const e2 = new WaitlistEngine();
  e2.addSession('A', 1);
  e2.register('甲', 'A', { priority: 9, preference: 1 });
  e2.register('乙', 'A', { priority: 1, preference: 1 });
  assert.throws(() => e2.revokeConfirmation('乙', 'A'), /仅已确认/);
});

console.log(`\n${passed} tests passed`);
