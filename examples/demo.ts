/**
 * 运行样例：复现验收边界场景。
 *   node examples/demo.ts   （需要 Node >= 22.18，原生支持 TypeScript）
 */
import { Channel, go } from '../src/channel.ts';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const log = (who: string, msg: string) => console.log(`[${who}] ${msg}`);

async function main() {
  const ch = new Channel<number>(2); // 容量为 2 的 Channel

  // 协程 P：写入 3 个数据
  const producer = go(async () => {
    for (const v of [1, 2, 3]) {
      log('P', `send(${v}) 开始`);
      await ch.send(v);
      log('P', `send(${v}) 完成`);
    }
  });

  // 协程 C：先休眠，让 P 阻塞在第 3 次写入
  const consumer = go(async () => {
    await sleep(50);
    log('C', `recv() -> ${JSON.stringify(await ch.recv())}  （P 应被唤醒完成第 3 次写入）`);
    await sleep(50);
    log('C', `recv() -> ${JSON.stringify(await ch.recv())}`);
    log('C', `recv() -> ${JSON.stringify(await ch.recv())}`);

    // 通道已空，接下来的读会挂起，直到 close 广播 EOF
    const waiting = go(async () => {
      log('R1', `recv() -> ${JSON.stringify(await ch.recv())}`);
    });
    const waiting2 = go(async () => {
      log('R2', `recv() -> ${JSON.stringify(await ch.recv())}`);
    });
    await sleep(50);
    log('main', 'close() 关闭通道，广播 EOF');
    ch.close();
    await Promise.all([waiting, waiting2]);
  });

  await Promise.all([producer, consumer]);
  log('main', '结束');
}

await main();
