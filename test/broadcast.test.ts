import { strict as assert } from 'assert';
import {
  Broadcast,
  BroadcastEvent,
  GapEvent,
  Subscription,
  UnsubscribedError,
} from '../src/broadcast';

type Ev = BroadcastEvent<number>;

function dataEvents(events: Ev[]): number[] {
  return events.filter((e) => e.kind === 'data').map((e) => (e as { seq: number }).seq);
}

const tests: Array<[string, () => void]> = [];
function test(name: string, fn: () => void): void {
  tests.push([name, fn]);
}

test('基本发布订阅：按序收到带序号的数据事件', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  bc.subscribe((e) => got.push(e));
  bc.publish(10);
  bc.publish(20);
  assert.deepEqual(got, [
    { kind: 'data', seq: 1, value: 10 },
    { kind: 'data', seq: 2, value: 20 },
  ]);
  assert.equal(bc.size, 1);
});

test('缓冲溢出：先收到缺口事件（丢失序号范围），再收到最新数据', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  // 容量 2；在第一个事件的回调里重入发布 3 条，缓冲 [2,3,4] 溢出 -> 丢弃 2、3
  bc.subscribe(
    (e) => {
      got.push(e);
      if (e.kind === 'data' && e.seq === 1) {
        bc.publish(2);
        bc.publish(3);
        bc.publish(4);
      }
    },
    { capacity: 2 },
  );
  bc.publish(1);
  assert.deepEqual(got, [
    { kind: 'data', seq: 1, value: 1 },
    { kind: 'gap', lostFrom: 2, lostTo: 2 },
    { kind: 'data', seq: 3, value: 3 },
    { kind: 'data', seq: 4, value: 4 },
  ]);
});

test('缺口范围跨多次溢出连续延伸', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  bc.subscribe(
    (e) => {
      got.push(e);
      if (e.kind === 'data' && e.seq === 1) {
        for (let i = 2; i <= 6; i++) bc.publish(i); // 容量 2 -> 丢弃 2,3,4
      }
    },
    { capacity: 2 },
  );
  bc.publish(1);
  assert.deepEqual(got, [
    { kind: 'data', seq: 1, value: 1 },
    { kind: 'gap', lostFrom: 2, lostTo: 4 },
    { kind: 'data', seq: 5, value: 5 },
    { kind: 'data', seq: 6, value: 6 },
  ]);
});

test('退订后不再收到事件，重复退订抛 UnsubscribedError', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  const sub = bc.subscribe((e) => got.push(e));
  bc.publish(1);
  sub.unsubscribe();
  assert.equal(sub.active, false);
  assert.equal(bc.size, 0);
  bc.publish(2);
  assert.equal(got.length, 1);
  assert.throws(() => sub.unsubscribe(), UnsubscribedError);
});

test('监听者可在回调中同步退订自己，之后不再收到事件', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  const sub: Subscription<number> = bc.subscribe((e) => {
    got.push(e);
    sub.unsubscribe();
  });
  bc.publish(1);
  bc.publish(2);
  assert.deepEqual(dataEvents(got), [1]);
});

test('监听者可在回调中退订其他订阅者，被退订者不接收当前事件', () => {
  const bc = new Broadcast<number>();
  const gotA: Ev[] = [];
  const gotB: Ev[] = [];
  let subB!: Subscription<number>;
  bc.subscribe((e) => {
    gotA.push(e);
    if (e.kind === 'data' && e.seq === 1) subB.unsubscribe();
  });
  subB = bc.subscribe((e) => gotB.push(e));
  bc.publish(1);
  bc.publish(2);
  assert.deepEqual(dataEvents(gotA), [1, 2]);
  assert.deepEqual(gotB, []); // A 先注册先交付，B 在交付前已被退订
});

test('本轮新订阅者不接收当前事件，从下一次发布开始接收', () => {
  const bc = new Broadcast<number>();
  const gotNew: Ev[] = [];
  bc.subscribe((e) => {
    if (e.kind === 'data' && e.seq === 1) {
      bc.subscribe((ev) => gotNew.push(ev));
    }
  });
  bc.publish(1);
  assert.deepEqual(gotNew, []); // 快照语义：收不到当前事件
  bc.publish(2);
  assert.deepEqual(dataEvents(gotNew), [2]);
});

test('监听者抛错不影响其他监听者，publish 汇总抛出 AggregateError', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  const boom = new Error('boom');
  bc.subscribe(() => {
    throw boom;
  });
  bc.subscribe((e) => got.push(e));
  let thrown: unknown;
  try {
    bc.publish(1);
  } catch (err) {
    thrown = err;
  }
  assert.ok(thrown instanceof AggregateError);
  assert.deepEqual((thrown as AggregateError).errors, [boom]);
  assert.deepEqual(dataEvents(got), [1]); // 其他监听者正常收到
});

test('重入发布：同一订阅者的事件仍按序号顺序交付', () => {
  const bc = new Broadcast<number>();
  const gotA: Ev[] = [];
  const gotB: Ev[] = [];
  bc.subscribe((e) => {
    gotA.push(e);
    if (e.kind === 'data' && e.seq === 1) bc.publish(2);
  });
  bc.subscribe((e) => gotB.push(e));
  bc.publish(1);
  bc.publish(3);
  assert.deepEqual(dataEvents(gotA), [1, 2, 3]);
  assert.deepEqual(dataEvents(gotB), [1, 2, 3]);
});

test('退订丢弃缓冲中未交付的事件', () => {
  const bc = new Broadcast<number>();
  const got: Ev[] = [];
  const sub = bc.subscribe(
    (e) => {
      got.push(e);
      if (e.kind === 'data' && e.seq === 1) {
        // 正在交付中，重入发布的 2、3 进入自身缓冲
        bc.publish(2);
        bc.publish(3);
        sub.unsubscribe(); // 退订：丢弃缓冲中未交付的 2、3
      }
    },
    { capacity: 8 },
  );
  bc.publish(1);
  bc.publish(4);
  // 只收到退订前已交付的 1；缓冲中的 2、3 被丢弃，也收不到 4
  assert.deepEqual(got, [{ kind: 'data', seq: 1, value: 1 }]);
});

test('非法容量抛 RangeError', () => {
  const bc = new Broadcast<number>();
  assert.throws(() => bc.subscribe(() => {}, { capacity: 0 }), RangeError);
  assert.throws(() => bc.subscribe(() => {}, { capacity: 1.5 }), RangeError);
});

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}
if (failed > 0) {
  console.error(`${failed} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
