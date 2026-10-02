import { Replica, versionGraphSize, stableStringify, type DottedEvent } from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function fmt<V>(replica: Replica<V>, key: string): string {
  const state = replica.state(key);
  const dots = state.values.map((d) => `${d.dot.node}#${d.dot.counter}`).join(', ');
  const ctx = Object.entries(state.context)
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([n, c]) => `${n}:${c}`)
    .join(' ');
  return `values=${JSON.stringify(replica.read(key).values)} liveDots=[${dots}] context={${ctx}}`;
}

console.log('=== 场景 A：热点键高频写入 + 多轮乱序/重复广播 ===');
{
  const rand = mulberry32(2026);
  const nodes = ['n1', 'n2', 'n3'].map((id) => new Replica<number>(id));
  const mailbox: Array<DottedEvent<number>> = [];
  let current = 0;
  let emptyWrites = 0;
  let overwriteWrites = 0;
  const TOTAL = 900;
  for (let round = 0; round < TOTAL; round++) {
    const writer = nodes[Math.floor(rand() * nodes.length)];
    let value: number;
    if (rand() < 0.7) {
      value = current; // 空更新：重复写相同值
      emptyWrites++;
    } else {
      value = current = Math.floor(rand() * 1000); // 覆盖更新
      overwriteWrites++;
    }
    const event = writer.write('hot-key', value);
    for (const replica of nodes) {
      if (replica === writer) continue;
      if (rand() < 0.8) mailbox.push(event);
      if (rand() < 0.25) mailbox.push(event); // 重复投递
    }
    if (round % 50 === 49) {
      for (const event of mailbox.splice(0).sort(() => rand() - 0.5)) {
        for (const replica of nodes) replica.receive(event);
      }
    }
  }
  for (const event of mailbox.splice(0).sort(() => rand() - 0.5)) {
    for (const replica of nodes) replica.receive(event);
  }
  console.log(`总写入 ${TOTAL} 次（空更新 ${emptyWrites}，覆盖更新 ${overwriteWrites}）`);
  const canonical = <V,>(replica: Replica<V>) =>
    stableStringify({
      values: replica.state('hot-key').values,
      context: Object.fromEntries(
        Object.entries(replica.state('hot-key').context).sort(([a], [b]) => (a < b ? -1 : 1)),
      ),
    });
  const reference = canonical(nodes[0]);
  const converged = nodes.every((n) => canonical(n) === reference);
  console.log(`三副本状态收敛: ${converged ? 'YES' : 'NO'}`);
  for (const node of nodes) console.log(`  ${node.id}: ${fmt(node, 'hot-key')}`);
  const size = versionGraphSize(nodes[0].state('hot-key'));
  console.log(`版本图体积: ${size}（活跃点 1 + 前置向量 ${size - 1} 项）`);
  console.log(`对比：逐事件保留分支的朴素方案将保留 ${TOTAL} 个分支`);
}

console.log('\n=== 场景 B：网络分区下的并发写 —— 同值去重 vs 异值兄弟 ===');
{
  const a = new Replica<string>('A');
  const b = new Replica<string>('B');
  const c = new Replica<string>('C');
  // 分区期间：A、B 写入相同值（重复写入），C 写入不同值（并发覆盖）
  const e1 = a.write('config', 'v2');
  const e2 = b.write('config', 'v2');
  const e3 = c.write('config', 'v3');
  // 分区恢复，事件乱序到达
  for (const replica of [a, b, c]) {
    for (const event of [e3, e1, e2]) {
      if (event !== (replica === a ? e1 : replica === b ? e2 : e3)) replica.receive(event);
    }
  }
  console.log('分区恢复后（未解决冲突）:');
  for (const replica of [a, b, c]) console.log(`  ${replica.id}: ${fmt(replica, 'config')}`);
  console.log('  => 相同值的两个并发写被去重为 1 个分支；不同值保留为真实兄弟，无幽灵分支');
  // 应用层解决冲突后写回
  const resolved = a.write('config', 'v3-resolved');
  b.receive(resolved);
  c.receive(resolved);
  console.log('冲突解决写回后:');
  for (const replica of [a, b, c]) console.log(`  ${replica.id}: ${fmt(replica, 'config')}`);
}
