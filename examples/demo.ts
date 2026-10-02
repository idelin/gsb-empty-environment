import { TemporalJoiner } from '../src/temporal-joiner.ts';
import type { JoinOutput } from '../src/temporal-joiner.ts';

function fmt(output: JoinOutput): string {
  switch (output.kind) {
    case 'matched':
      return `MATCHED key=${output.key} (L@${output.left.ts} + R@${output.right.ts})${output.late ? ' [late 补配]' : ''}`;
    case 'left-only':
      return `LEFT-ONLY key=${output.key} (L@${output.left.ts} 超时孤立)`;
    case 'retract-left-only':
      return `RETRACT-LEFT-ONLY key=${output.key} (撤回之前的孤立输出)`;
  }
}

const joiner = new TemporalJoiner({ windowMs: 5000 });

function step(label: string, action: () => void): void {
  action();
  const outputs = joiner.drain();
  console.log(label.padEnd(34), outputs.length === 0 ? '→ (无输出)' : '');
  for (const o of outputs) console.log(''.padEnd(34), `→ ${fmt(o)}`);
}

console.log('=== 双流时序 Join（窗口 5s，带取消事件）===\n');

step('t=0    LEFT  L1', () => joiner.left({ key: '1', ts: 0 }));
step('t=3    RIGHT R1', () => joiner.right({ key: '1', ts: 3000 }));
step('t=10   LEFT  L2', () => joiner.left({ key: '2', ts: 10_000 }));
step('t=12   CANCEL L2', () => joiner.cancel('2'));
step('t=15   (L2 原超时点)', () => joiner.advanceTime(15_000));
step('t=20   LEFT  L3', () => joiner.left({ key: '3', ts: 20_000 }));
step('t=25   (L3 超时)', () => joiner.advanceTime(25_000));
step('t=27   RIGHT R3@24 (迟到)', () => joiner.right({ key: '3', ts: 24_000 }));
step('t=100  (推进水位线, 清理状态)', () => joiner.advanceTime(100_000));

console.log('\n=== 最终状态（应全部归零，零泄漏）===');
console.log(joiner.stats());
