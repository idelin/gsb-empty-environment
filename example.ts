import { EventBus } from './event-bus.ts';

interface RouteChange {
  to: string;
}

const bus = new EventBus<RouteChange>();

// 页面 A 的组件在自己的 scope 里订阅
const pageA = bus.scope();
pageA.subscribe(({ to }) => console.log(`[PageA] navigate -> ${to}`));
pageA.once(() => console.log('[PageA] first event only'));

// 页面 B 的组件
const pageB = bus.scope();
pageB.subscribe(({ to }) => console.log(`[PageB] navigate -> ${to}`));

console.log('--- emit #1 (both pages active) ---');
bus.emit({ to: '/dashboard' });

// 切换页面：关闭 A 的 scope，旧组件立即退订，不会再收到事件
pageA.close();

console.log('--- emit #2 (PageA disposed) ---');
const r1 = bus.emit({ to: '/settings' });
console.log(`invoked=${r1.invoked} skipped=${r1.skipped} errors=${r1.errors.length} active=${bus.size}`);

// AbortSignal 绑定组件生命周期
const ac = new AbortController();
bus.subscribe(({ to }) => console.log(`[signal-bound] ${to}`), { signal: ac.signal });
console.log(`after subscribe with signal, active=${bus.size}`);
ac.abort(); // 组件卸载
console.log(`after abort, active=${bus.size}`);

// 异常收集：坏监听器不影响其他监听器
bus.subscribe(() => {
  throw new Error('listener bug');
});
bus.subscribe(({ to }) => console.log(`[survivor] still called for ${to}`));
const r2 = bus.emit({ to: '/error-demo' });
console.log(`errors collected: ${r2.errors.length}, first: ${(r2.errors[0].error as Error).message}`);

// 页面 B 卸载，计数归零
pageB.close();
console.log(`after PageB close, active=${bus.size}`);
