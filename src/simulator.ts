import { Checker } from './checker';
import { UnreliableChannel } from './channel';
import { runScenarios } from './scenarios';
import { SourceNode } from './source';
import { TargetNode } from './target';

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** 可复现的伪随机数发生器 */
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

export async function runSimulation(seed: number): Promise<void> {
  const rng = mulberry32(seed);
  const rand = (n: number) => Math.floor(rng() * n);
  const source = new SourceNode({ latencyMs: () => rand(3) });
  const target = new TargetNode();
  const checker = new Checker(source);
  const channel = new UnreliableChannel(rng);

  const KEYSPACE = 40;
  const PRELOAD = 30; // 复制开始前就存在的数据
  const WRITES = 240; // 复制期间客户端发起的写
  const FENCE_AT = 160; // 第 N 次发起后落下栅栏

  // 预置数据（复制开始前已确认）
  for (let i = 0; i < PRELOAD; i++) {
    const res = await source.write(`k${rand(KEYSPACE)}`, `init-${i}`);
    if (res.ok) {
      checker.recordAck(res.op);
    }
  }

  let clientDone = false;
  let ackedAfterFence = 0;

  const client = (async () => {
    const pending: Array<Promise<void>> = [];
    for (let i = 0; i < WRITES; i++) {
      if (i === FENCE_AT) {
        // 先在栅栏前制造一批在途请求，再落栅栏：它们必须正常返回结果
        for (let j = 0; j < 3; j++) {
          const p = source.write(`k${rand(KEYSPACE)}`, `inflight-${j}`).then((res) => {
            if (res.ok) {
              checker.recordAck(res.op);
              if (source.isFenced) {
                ackedAfterFence++;
              }
            }
          });
          pending.push(p);
        }
        source.fence(); // 切换栅栏：甲不再接受新写
      }
      const key = `k${rand(KEYSPACE)}`;
      const isDel = rng() < 0.25;
      const p = source.write(key, isDel ? null : `v${i}`).then((res) => {
        if (res.ok) {
          checker.recordAck(res.op);
        } else {
          checker.recordReject();
        }
      });
      pending.push(p);
      if (pending.length > 8) {
        await pending.shift(); // 限制并发，保持在途请求
      }
      await sleep(rand(2));
    }
    await Promise.all(pending);
    // 栅栏后再发几次，确认甲拒绝新写
    for (let i = 0; i < 5; i++) {
      const res = await source.write(`ghost-${i}`, 'x');
      if (res.ok) {
        throw new Error('栅栏后甲仍接受新写');
      }
      checker.recordReject();
    }
    clientDone = true;
  })();

  const replicator = (async () => {
    // 阶段 1：基础数据复制（期间甲仍在写入，允许读到不一致的中间态）
    for (const key of source.listKeys()) {
      await sleep(rand(2));
      const e = source.readEntry(key);
      if (e) {
        target.apply(e.key, e.entry);
      }
      checker.checkStep(target, '基础复制');
    }
    // 阶段 2：增量复制（乱序 + 重发通道），逐步检查
    let cursor = 0;
    for (;;) {
      for (const op of source.oplogAfter(cursor)) {
        channel.send({
          key: op.key,
          entry: { seq: op.seq, value: op.kind === 'del' ? null : op.value },
        });
        cursor = op.seq;
      }
      const n = rand(4);
      for (let i = 0; i < n; i++) {
        channel.deliverOne(target);
      }
      checker.checkStep(target, '增量复制');
      if (clientDone && source.inflightCount === 0 && cursor === source.highWaterSeq) {
        break;
      }
      await sleep(rand(2));
    }
    // 阶段 3：切换前排空通道（含重发副本）
    channel.drain(target);
    checker.checkStep(target, '排空');
  })();

  await Promise.all([client, replicator]);

  if (ackedAfterFence === 0) {
    throw new Error('未覆盖到“栅栏后在途请求仍返回结果”的路径');
  }
  checker.checkFinal(target);
  const s = checker.stats;
  console.log(
    `seed=${seed} 通过：已确认写 ${s.acked}（栅栏后返回 ${ackedAfterFence}），` +
      `栅栏拒绝 ${s.rejected}，乙最终 key 数=${target.keys().length}`,
  );
}

async function main(): Promise<void> {
  await runScenarios();
  for (const seed of [1, 2, 3, 4, 5]) {
    await runSimulation(seed);
  }
  console.log('全部检查通过：已确认写最终在乙可见，删除未复活');
}

main().catch((err) => {
  console.error('模拟失败：', err);
  process.exit(1);
});
