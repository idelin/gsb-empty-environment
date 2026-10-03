/**
 * 运行样例:完整复现验收边界场景。
 *
 *   t=0s   客户端 1 获得文件 X 为期 5 秒的读租约
 *   t=2s   客户端 2 尝试修改 X
 *          服务端通知客户端 1 撤销租约,但客户端 1 失联
 *   t=5s   租约自然过期,服务端放行客户端 2 的写入
 *
 * 运行:npm run demo
 */
import { setTimeout as sleep } from "node:timers/promises";
import { LeaseServer } from "./src/leaseServer.ts";
import { CacheClient } from "./src/client.ts";

const t0 = Date.now();
const ts = () => `t=${((Date.now() - t0) / 1000).toFixed(1)}s`;
const log = (msg: string) => console.log(`[${ts()}] ${msg}`);

const server = new LeaseServer();
const client1 = new CacheClient("client-1", server);
const client2 = new CacheClient("client-2", server);

await client2.write("X", "v0-初始内容");
log(`client-2 初始化 X = "v0-初始内容"`);

const data = await client1.read("X");
log(`client-1 获得 X 的 5 秒读租约,缓存 "${data}"`);

client1.simulateUnreachable = true;
log(`client-1 失联(不响应撤销回调)`);

await sleep(2_000 - (Date.now() - t0));
log(`client-2 尝试写入 "v1-修改后" ...`);
const writePromise = client2.write("X", "v1-修改后");

await sleep(500);
log(`写入阻塞中... 服务端已发出 ${server.revokeNotifications} 次撤销通知,` +
    `client-1 本地缓存仍可读:"${await client1.read("X")}"(租约保护期内,安全)`);

const result = await writePromise;
log(`租约期满,写入放行!阻塞 ${(result.waitedMs / 1000).toFixed(1)}s,` +
    `X 现为版本 v${result.version}`);

const client3 = new CacheClient("client-3", server);
log(`client-3 读取 X = "${await client3.read("X")}"(零读脏:必为新值)`);
log(`client-1 租约已过期,重新读取 X = "${await client1.read("X")}"`);
