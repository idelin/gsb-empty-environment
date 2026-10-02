/**
 * 运行样例：持久化响应式存储 + 增量 Hydration。
 *
 *   node examples/demo.ts
 *
 * 阶段一（进程 A）：构建状态树（持久化 + 临时），挂载派生 Effect，
 * 修改状态后生成 JSON 检查点，随后通过 child_process 启动全新进程 B。
 * 阶段二（进程 B）：重新声明状态树（全部为初始值），加载检查点，
 * 持久化状态精确复原、临时状态安全重置、派生 Effect 自动重新对齐。
 */
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { ReactiveStore, persist } from '../src/store.ts';

const CHECKPOINT = new URL('./checkpoint.json', import.meta.url).pathname;

function buildStore() {
  const store = new ReactiveStore();

  // 持久化状态（带 @persist 注解）
  const user = store.signal('user', { name: 'anonymous', loginCount: 0 }, persist);
  const theme = store.signal('theme', 'light', persist);

  // 临时状态（不持久化，重启后安全重置）
  const draft = store.signal('draft', '<empty>');
  const sessionToken = store.signal('sessionToken', '<none>');

  // 派生状态 + Effect 依赖链
  const greeting = store.computed(() => `你好, ${user.get().name}!`);
  store.effect(() => {
    console.log(
      `  [effect] greeting="${greeting.get()}" theme=${theme.get()} ` +
        `loginCount=${user.get().loginCount} draft=${draft.get()} token=${sessionToken.get()}`,
    );
  });

  return { store, user, theme, draft, sessionToken };
}

function phaseCheckpoint(): void {
  console.log('[进程 A] 构建状态树并修改状态…');
  const { store, user, theme, draft, sessionToken } = buildStore();

  user.set({ name: 'delin', loginCount: 42 });
  theme.set('dark');
  draft.set('未保存的草稿'); // 临时状态：不应进入检查点
  sessionToken.set('sess-abc123'); // 临时状态：不应进入检查点

  const snapshot = store.checkpoint(CHECKPOINT);
  console.log('[进程 A] 已生成检查点：');
  console.log(JSON.stringify(snapshot, null, 2));
}

function phaseRestore(): void {
  console.log('[进程 B] 全新进程，状态树回到初始值，开始 Hydration…');
  const { store, user, theme, draft, sessionToken } = buildStore();

  const report = store.hydrate(CHECKPOINT);
  console.log('[进程 B] Hydration 报告：', JSON.stringify(report));
  console.log('[进程 B] 最终状态：', JSON.stringify({
    user: user.get(),
    theme: theme.get(),
    draft: draft.get(),
    sessionToken: sessionToken.get(),
  }));
}

function main(): void {
  const mode = process.argv[2];
  if (mode === '--restore') {
    phaseRestore();
    return;
  }
  phaseCheckpoint();
  console.log('[进程 A] 退出，启动全新进程 B 模拟系统重启…\n');
  const out = execFileSync(process.execPath, [fileURLToPath(import.meta.url), '--restore'], {
    encoding: 'utf8',
  });
  process.stdout.write(out);
}

main();
