import { signal, computed, effect, TimeTravelController } from '../src/index.ts';

const count = signal(0);
const step = signal(1);
const double = computed(() => count.get() * 2);

console.log('--- UI 渲染日志（effect 自动触发） ---');
effect(() => {
  console.log(`[render] count=${count.get()} step=${step.get()} double=${double.get()}`);
});

const tt = new TimeTravelController();

console.log('\n--- 连续 3 次用户修改 ---');
count.set(1);
count.set(2);
step.set(5);

console.log('\n--- Undo × 2（回到第 1 次修改后） ---');
tt.undo();
tt.undo();

console.log('\n--- Redo × 1（前进到第 2 次修改后） ---');
tt.redo();

console.log('\n--- jumpTo 初始快照 #0 ---');
tt.jumpTo(0);

console.log('\n--- 历史快照树 ---');
for (const s of tt.snapshots()) {
  const values = [...s.values.entries()]
    .map(([id, v]) => `signal#${id}=${JSON.stringify(v)}`)
    .join(', ');
  console.log(`#${s.id} "${s.label}" parent=${s.parentId} children=[${s.childIds}] | ${values}`);
}
console.log(`\n当前位置: #${tt.current.id} canUndo=${tt.canUndo} canRedo=${tt.canRedo}`);
