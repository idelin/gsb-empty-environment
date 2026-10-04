import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Broadcast, type BroadcastEvent, type Listener } from '../src/broadcast.ts';

function recorder<T>() {
  const events: BroadcastEvent<T>[] = [];
  const listener: Listener<T> = (e) => {
    events.push(e);
  };
  return { events, listener };
}

test('基本发布订阅：所有订阅者按序收到 data 事件，seq 全局递增', () => {
  const bc = new Broadcast<string>();
  const a = recorder<string>();
  const b = recorder<string>();
  bc.subscribe(a.listener);
  bc.subscribe(b.listener);

  bc.publish('hello');
  bc.publish('world');

  const expected: BroadcastEvent<string>[] = [
    { kind: 'data', seq: 1, value: 'hello' },
    { kind: 'data', seq: 2, value: 'world' },
  ];
  assert.deepEqual(a.events, expected);
  assert.deepEqual(b.events, expected);
});

test('缓冲溢出：先交付 gap 事件（丢失序号闭区间），再继续最新数据', () => {
  const bc = new Broadcast<string>({ capacity: 2 });
  const a = recorder<string>();
  const b = recorder<string>();

  // A 在收到第一条时重入发布 3 条，A 的缓冲（容量 2）装不下 → 溢出
  const listenerA: Listener<string> = (e) => {
    a.events.push(e);
    if (e.kind === 'data' && e.seq === 1) {
      bc.publish('x'); // seq 2
      bc.publish('y'); // seq 3
      bc.publish('z'); // seq 4
    }
  };
  bc.subscribe(listenerA);
  bc.subscribe(b.listener);

  bc.publish('a'); // seq 1

  // A 的缓冲容量 2：seq2 被挤出，先收到 gap(2,2)，再收到最新的 seq3、seq4
  assert.deepEqual(a.events, [
    { kind: 'data', seq: 1, value: 'a' },
    { kind: 'gap', from: 2, to: 2 },
    { kind: 'data', seq: 3, value: 'y' },
    { kind: 'data', seq: 4, value: 'z' },
  ]);
  // B 容量充足，完整收到全部事件
  assert.deepEqual(b.events, [
    { kind: 'data', seq: 1, value: 'a' },
    { kind: 'data', seq: 2, value: 'x' },
    { kind: 'data', seq: 3, value: 'y' },
    { kind: 'data', seq: 4, value: 'z' },
  ]);
});

test('连续溢出：丢失区间合并为连续闭区间，gap 之后序号衔接', () => {
  const bc = new Broadcast<number>({ capacity: 1 });
  const events: BroadcastEvent<number>[] = [];
  bc.subscribe((e) => {
    events.push(e);
    if (e.kind === 'data' && e.seq === 1) {
      bc.publish(2);
      bc.publish(3);
      bc.publish(4);
    }
  });

  bc.publish(1);

  assert.deepEqual(events, [
    { kind: 'data', seq: 1, value: 1 },
    { kind: 'gap', from: 2, to: 3 },
    { kind: 'data', seq: 4, value: 4 },
  ]);
  // gap 之后的第一条数据序号 = gap.to + 1
  const gap = events[1];
  const next = events[2];
  assert.ok(gap.kind === 'gap' && next.kind === 'data');
  assert.equal(next.seq, gap.to + 1);
});

test('退订后监听器不再被调用；unsubscribe 幂等', () => {
  const bc = new Broadcast<number>();
  const a = recorder<number>();
  const sub = bc.subscribe(a.listener);

  bc.publish(1);
  assert.equal(a.events.length, 1);
  assert.equal(sub.closed, false);

  sub.unsubscribe();
  assert.equal(sub.closed, true);
  assert.equal(bc.subscriberCount, 0);

  bc.publish(2);
  bc.publish(3);
  assert.equal(a.events.length, 1); // 没有新事件

  assert.doesNotThrow(() => sub.unsubscribe()); // 幂等
});

test('监听器可在回调中同步退订自己，当前轮剩余事件不再交付', () => {
  const bc = new Broadcast<number>({ capacity: 8 });
  const events: BroadcastEvent<number>[] = [];
  const sub = bc.subscribe((e) => {
    events.push(e);
    if (e.kind === 'data' && e.seq === 1) {
      sub.unsubscribe();
      bc.publish(2); // 重入发布：已退订，不应再收到
      bc.publish(3);
    }
  });

  bc.publish(1);
  assert.deepEqual(events, [{ kind: 'data', seq: 1, value: 1 }]);
});

test('监听器可在回调中同步退订其他订阅者，立即生效', () => {
  const bc = new Broadcast<string>();
  const b = recorder<string>();
  const subA = bc.subscribe(() => {
    subB.unsubscribe(); // A 先交付，退订 B
  });
  const subB = bc.subscribe(b.listener);

  bc.publish('first');
  assert.deepEqual(b.events, []); // B 未收到当前事件

  subA.unsubscribe();
});

test('发布过程中新订阅的监听器不接收当前事件，从下一次发布开始接收', () => {
  const bc = new Broadcast<string>();
  const late = recorder<string>();
  bc.subscribe((e) => {
    if (e.kind === 'data' && e.seq === 1) {
      bc.subscribe(late.listener); // 本轮新订阅
    }
  });

  bc.publish('first');
  assert.deepEqual(late.events, []); // 不接收当前事件

  bc.publish('second');
  assert.deepEqual(late.events, [{ kind: 'data', seq: 2, value: 'second' }]);
});

test('监听器抛错不中断其他订阅者，publish 汇总抛出 AggregateError', () => {
  const bc = new Broadcast<string>();
  const b = recorder<string>();
  let shouldThrow = true;
  bc.subscribe(() => {
    if (shouldThrow) {
      shouldThrow = false;
      throw new Error('boom');
    }
  });
  bc.subscribe(b.listener);

  assert.throws(
    () => bc.publish('x'),
    (err: unknown) => {
      assert.ok(err instanceof AggregateError);
      assert.equal(err.errors.length, 1);
      assert.ok(err.errors[0] instanceof Error);
      assert.equal((err.errors[0] as Error).message, 'boom');
      return true;
    },
  );
  // 抛错的订阅者不妨碍 B 收到事件
  assert.deepEqual(b.events, [{ kind: 'data', seq: 1, value: 'x' }]);

  // 广播实例仍可正常使用
  bc.publish('y');
  assert.deepEqual(b.events[1], { kind: 'data', seq: 2, value: 'y' });
});

test('重入发布保持每个订阅者的事件序号顺序', () => {
  const bc = new Broadcast<number>({ capacity: 16 });
  const a = recorder<number>();
  const b = recorder<number>();
  bc.subscribe((e) => {
    a.events.push(e);
    if (e.kind === 'data' && e.seq === 1) {
      bc.publish(2);
      bc.publish(3);
    }
  });
  bc.subscribe(b.listener);

  bc.publish(1);

  const seqsOf = (events: BroadcastEvent<number>[]) =>
    events.map((e) => (e.kind === 'data' ? e.seq : -1));
  assert.deepEqual(seqsOf(a.events), [1, 2, 3]);
  assert.deepEqual(seqsOf(b.events), [1, 2, 3]);
});

test('capacity 必须为正整数', () => {
  assert.throws(() => new Broadcast({ capacity: 0 }), RangeError);
  assert.throws(() => new Broadcast({ capacity: 1.5 }), RangeError);
  assert.throws(() => new Broadcast({ capacity: -3 }), RangeError);
});
