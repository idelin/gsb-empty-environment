import {
  SessionWindowMerger,
  type WindowChange,
} from '../src/session-windows.ts';

function printChanges(action: string, changes: WindowChange[]): void {
  console.log(`\n== ${action} ==`);
  if (changes.length === 0) {
    console.log('  (无窗口变更)');
    return;
  }
  for (const c of changes) {
    const w = c.window;
    console.log(
      `  ${c.kind.padEnd(7)} window=[${w.start}, ${w.end}] count=${w.count}`,
    );
  }
}

// 验收场景：gap=5，maxLateness=10
const merger = new SessionWindowMerger({ gap: 5, maxLateness: 10 });

printChanges('事件 t=0 到达', merger.addEvent('e0', 0));
printChanges('事件 t=9 到达（间隔 9 > gap=5，独立窗口）', merger.addEvent('e9', 9));
printChanges('迟到事件 t=4 到达（桥接两窗口）', merger.addEvent('e4', 4));
printChanges('删除事件 t=4（窗口重新分裂）', merger.removeEvent('e4'));

printChanges('水位线推进到 30（丢弃超出最大延迟的状态）', []);
const discarded = merger.advanceWatermark(30);
for (const w of discarded) {
  console.log(`  purged   window=[${w.start}, ${w.end}]（已最终确定，不再发 retract）`);
}
console.log(`  剩余状态事件数: ${merger.stateSize}`);

printChanges(
  '过于迟到的事件 t=5（< watermark 30 - maxLateness 10，被丢弃）',
  merger.addEvent('late', 5),
);
