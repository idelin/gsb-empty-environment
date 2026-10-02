import { setTimeout as sleep } from "node:timers/promises";
import { LockServer } from "../src/lock-server.ts";
import { LockClient } from "../src/lock-client.ts";
import { FencedStore } from "../src/fenced-store.ts";

const start = Date.now();
function log(msg: string): void {
  const t = ((Date.now() - start) / 1000).toFixed(1).padStart(5);
  console.log(`[+${t}s] ${msg}`);
}

const TTL = 5000;
const server = new LockServer();
const store = new FencedStore();
const nodeA = new LockClient(server, "node-A");
const nodeB = new LockClient(server, "node-B");

// 1. 节点 A 获取锁，后台心跳自动续期
const handleA = await nodeA.acquire("shared-resource", {
  ttlMs: TTL,
  heartbeatIntervalMs: 1500,
});
log(`[A] 获取锁成功 fenceToken=${handleA.fenceToken}，TTL=${TTL}ms，心跳已启动`);
handleA.on("renewed", (lease) => {
  const remain = ((lease.expiresAt - Date.now()) / 1000).toFixed(1);
  log(`[A] 心跳续期成功，租约剩余约 ${remain}s`);
});
handleA.on("lost", (err) => {
  log(`[A] 租约丢失：${(err as Error).message}`);
});
store.write(handleA.fenceToken, "balance", "A:100");
log(`[A] 写入 balance=A:100 (fenceToken=${handleA.fenceToken})`);

// 2. 节点 B 请求同一把锁，进入等待队列
log(`[B] 请求锁，进入等待队列…`);
const bPromise = nodeB.acquire("shared-resource", { ttlMs: TTL });

// 3. 3.2s 后 A 假死挂起（此前已有两次成功心跳）
await sleep(3200);
handleA.suspend();
log(`[A] *** 节点假死：心跳停止（模拟崩溃/网络分区） ***`);

// 4. 最后一次心跳的 5 秒后租约自然超时，B 被唤醒获锁
const handleB = await bPromise;
log(`[B] A 的租约已超时，获取锁成功 fenceToken=${handleB.fenceToken}`);

// 5. A 恢复，发现租约已丢失
handleA.resume();
log(`[A] 节点恢复，isHeld=${handleA.isHeld}（租约已丢失）`);

// 6. A 携带过期 fence token 尝试写共享资源 → 被拒绝
try {
  handleA.ensureHeld();
  store.write(handleA.fenceToken, "balance", "A:999");
  log(`[A] 写入成功（不应发生！）`);
} catch (err) {
  log(`[A] 写操作被拒绝 → ${(err as Error).name}: ${(err as Error).message}`);
}

// 6b. 即使僵尸节点绕过客户端检查直接写存储，也会被 fencing token 拦截
try {
  store.write(handleA.fenceToken, "balance", "A:999");
} catch (err) {
  log(`[A] 绕过客户端直写存储仍被拒绝 → ${(err as Error).name}: ${(err as Error).message}`);
}

// 7. B 携带新 fence token 正常写入
store.write(handleB.fenceToken, "balance", "B:200");
log(`[B] 写入 balance=B:200 (fenceToken=${handleB.fenceToken})，当前值=${store.read("balance")}`);

handleB.release();
server.close();
log("演示结束");
