/**
 * 运行样例：响应式循环更新守护器
 * 运行方式：node examples/demo.ts   （需要 Node >= 22.6，推荐 >= 23.6）
 */
import { signal, computed, effect, CircularSignalUpdate } from '../src/index.ts';

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// 1. 正常用法：有限次迭代，收敛
section('1. 正常用法（收敛，不触发守护）');
const count = signal(0, { name: 'Count' });
effect(() => {
  const value = count.get();
  if (value < 5) {
    count.set(value + 1);
  }
}, { name: 'counter' });
console.log(`Count 收敛于: ${count.get()}`);

// 2. 验收场景：Effect 读取 Signal A 又写入 Signal A -> 第 50 次迭代被拦截
section('2. 死循环场景（Effect 读写 Signal A）');
const a = signal(0, { name: 'A' });
try {
  effect(() => {
    a.set(a.get() + 1);
  }, { name: 'self-increment' });
  console.log('未被拦截（不应出现）');
} catch (error) {
  if (error instanceof CircularSignalUpdate) {
    console.log(`已捕获 ${error.name}，A 最终值 = ${a.get()}\n`);
    console.log(error.message);
  } else {
    throw error;
  }
}

// 3. 调用栈环路：Computed 互相读取
section('3. 调用栈环路（Computed 互相依赖）');
const c1Ref: { current?: { get(): number } } = {};
const c2Ref: { current?: { get(): number } } = {};
c1Ref.current = computed(() => (c2Ref.current as { get(): number }).get() + 1, { name: 'C1' });
c2Ref.current = computed(() => (c1Ref.current as { get(): number }).get() + 1, { name: 'C2' });
try {
  (c1Ref.current as { get(): number }).get();
} catch (error) {
  if (error instanceof CircularSignalUpdate) {
    console.log(error.message.split('\n').slice(0, 4).join('\n'));
  } else {
    throw error;
  }
}

console.log('\n演示结束。');
