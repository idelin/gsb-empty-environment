import { Channel } from "../src/channel.ts";

function log(msg: string): void {
  console.log(`[${new Date().toISOString().slice(11, 23)}] ${msg}`);
}

function tick(times = 1): Promise<void> {
  return new Promise((resolve) => {
    const step = (n: number) => (n <= 0 ? resolve() : setImmediate(() => step(n - 1)));
    step(times);
  });
}

async function main(): Promise<void> {
  log("=== 场景1：容量为2的Channel，P写入3个数据 ===");
  const ch = new Channel<number>(2);

  const P = (async () => {
    for (const v of [1, 2, 3]) {
      log(`P: 准备写入 ${v}（缓冲区 ${ch.length}/2）`);
      await ch.send(v);
      log(`P: 写入 ${v} 完成`);
    }
    log("P: 全部写入完成");
  })();

  await tick();
  log(`main: P 已挂起（等待发送者=${ch.pendingSenders}），启动消费者 C`);

  const C = (async () => {
    const r = await ch.recv();
    log(`C: 读取到 ${r.value}`);
  })();

  await tick();
  await Promise.all([P, C]);

  log("=== 场景2：关闭通道，所有等待读协程收到 EOF ===");
  const readers = ["R1", "R2"].map((name) =>
    (async () => {
      for (;;) {
        const r = await ch.recv();
        if (r.done) {
          log(`${name}: 收到 EOF，退出`);
          return;
        }
        log(`${name}: 读取到 ${r.value}`);
      }
    })(),
  );

  await tick();
  log(`main: 等待中的接收协程数=${ch.pendingReceivers}，执行 close()`);
  ch.close();
  await Promise.all(readers);
  log("main: 全部协程结束");
}

main();
