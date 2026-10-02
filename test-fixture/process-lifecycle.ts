/** 跨进程测试夹具：write 模式生成检查点，read 模式恢复并输出结果 JSON。 */
import { ReactiveStore, persist } from '../src/store.ts';

const [mode, file] = process.argv.slice(2);

if (mode === 'write') {
  const store = new ReactiveStore();
  const count = store.signal('count', 0, persist);
  const theme = store.signal('theme', 'light', persist);
  const temp = store.signal('temp', 'init');
  count.set(42);
  theme.set('dark');
  temp.set('should-not-persist');
  store.checkpoint(file);
} else {
  const store = new ReactiveStore();
  const count = store.signal('count', 0, persist);
  const theme = store.signal('theme', 'light', persist);
  const temp = store.signal('temp', 'init');
  const derived = store.computed(() => `count=${count.get()}/theme=${theme.get()}`);
  let effectRuns = 0;
  let lastDerived = '';
  store.effect(() => {
    effectRuns += 1;
    lastDerived = derived.get();
  });
  store.hydrate(file);
  console.log(JSON.stringify({
    persisted: { count: count.get(), theme: theme.get() },
    temp: temp.get(),
    effectRuns,
    lastDerived,
  }));
}
