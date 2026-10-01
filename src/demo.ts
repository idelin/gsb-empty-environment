/**
 * 运行样例：模拟验收场景的事件流，打印每一步输出。
 * 运行：npm run demo
 */
import { TemporalJoiner } from './joiner.ts';
import type { JoinOutput, StreamEvent } from './joiner.ts';

const joiner = new TemporalJoiner({ windowMs: 5000 });

function fmt(output: JoinOutput): string {
  switch (output.type) {
    case 'matched':
      return `MATCHED      key=${output.key} (L@${output.left.ts}, R@${output.right.ts})`;
    case 'left-only':
      return `LEFT-ONLY    key=${output.key} (L@${output.left.ts} 超时孤立)`;
    case 'late-matched':
      return `LATE-MATCHED key=${output.key} (L@${output.left.ts}, 迟到 R@${output.right.ts} 补配)`;
  }
}

function emit(outputs: JoinOutput[]): void {
  for (const o of outputs) console.log(`    -> 输出 ${fmt(o)}`);
  if (outputs.length === 0) console.log('    -> (无输出)');
}

function feed(label: string, event: StreamEvent): void {
  console.log(`t=${String(event.ts).padStart(6)}  ${label}`);
  emit(joiner.process(event));
}

function advance(ts: number): void {
  console.log(`t=${String(ts).padStart(6)}  [水位线推进到 ${ts}]`);
  emit(joiner.advanceWatermark(ts));
}

console.log('=== 场景 1：正常配对（L1@0，R1@3）===');
feed('左流  L1', { kind: 'left', key: 'L1', ts: 0 });
feed('右流  R1', { kind: 'right', key: 'L1', ts: 3000 });

console.log('\n=== 场景 2：取消事件（L2@10s，取消@12s，15s 超时不输出）===');
feed('左流  L2', { kind: 'left', key: 'L2', ts: 10_000 });
feed('取消  L2', { kind: 'cancel', key: 'L2', ts: 12_000 });
advance(15_000);

console.log('\n=== 场景 3：超时孤立（L3@20s，无匹配）===');
feed('左流  L3', { kind: 'left', key: 'L3', ts: 20_000 });
advance(25_000);

console.log('\n=== 场景 4：迟到补配（L4@30s 已超时，R4 事件时间 33s 晚到）===');
feed('左流  L4', { kind: 'left', key: 'L4', ts: 30_000 });
advance(35_000);
feed('右流  R4（迟到）', { kind: 'right', key: 'L4', ts: 33_000 });

console.log('\n=== 场景 5：乱序（R5@41s 先于 L5@40s 到达）===');
feed('右流  R5（乱序先到）', { kind: 'right', key: 'L5', ts: 41_000 });
feed('左流  L5', { kind: 'left', key: 'L5', ts: 40_000 });

console.log('\n=== 收尾：水位线推进到远端，验证状态零泄漏 ===');
advance(1_000_000);
console.log(`    内部状态条目数 = ${joiner.getStateSize()}（期望 0）`);
