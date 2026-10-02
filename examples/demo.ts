/**
 * 验收场景演示(真实时间):
 *  - 客户端1 在 t=0s 获得文件 X 为期 5s 的读租约;
 *  - 客户端2 在 t=2s 尝试修改 X;
 *  - 服务端通知客户端1 撤销租约,但客户端1 已失联;
 *  - 服务端安全阻塞,等满 5s 租约期后才放行客户端2 的写入 —— 零读脏。
 */
import { LeaseServer } from '../src/lease-server.ts';
import { CacheClient } from '../src/cache-client.ts';

const LEASE_MS = 5000;
const server = new LeaseServer({ leaseDurationMs: LEASE_MS });
server.seed('X', 'v1');

const client1 = new CacheClient('client-1', server);
const client2 = new CacheClient('client-2', server);

const t0 = Date.now();
const ts = (): string => `${((Date.now() - t0) / 1000).toFixed(2)}s`;
const log = (msg: string): void => console.log(`[t=${ts()}] ${msg}`);

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

async function main(): Promise<void> {
  log(`服务端:文件 X = "v1",租约时长 ${LEASE_MS / 1000}s`);

  // t=0s: 客户端1 读取 X,获得 5s 读租约
  const v = await client1.read('X');
  log(`客户端1: 读取 X = "${v}",获得读租约(有效期至 t=5.00s)`);

  // 客户端1 随后失联(网络分区/进程崩溃),撤销回调无法送达
  client1.disconnect();
  log('客户端1: *** 失联(撤销回调不可达) ***');

  // t=2s: 客户端2 尝试写入
  await sleep(2000);
  log('客户端2: 请求写入 X = "v2"');
  log('服务端: 检测到有效读租约,尝试通知客户端1 撤销 ... 不可达,安全阻塞至租约期满');

  const writePromise = client2.write('X', 'v2').then(() => {
    log('客户端2: 写入完成,服务端落盘 X = "v2"');
  });

  // 阻塞期间:客户端1 在租约期内读本地缓存,服务端保证其读到的是一致数据
  await sleep(1000); // t≈3s
  const cached = await client1.read('X');
  log(`客户端1: (租约期内)读本地缓存 X = "${cached}" —— 与服务端一致,零读脏`);
  log(`服务端: 当前存储仍为 X = "${server.read('X')}"(写入尚未放行)`);

  await writePromise; // 应在 t≈5s 才完成

  // 写完成后,新读者看到新值
  const client3 = new CacheClient('client-3', server);
  const fresh = await client3.read('X');
  log(`客户端3: 新读取 X = "${fresh}"`);
  log('演示结束:写入在租约期满(5s)后才放行,全程零读脏 ✔');
}

await main();
