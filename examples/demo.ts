/**
 * 演示：一阶低通 IIR（y[n] = 0.25·x[n] + 0.75·y[n−1]），缺口阈值 2。
 * 场景：传感器数据流中出现长缺口，随后迟到样本把缺口拆短、两段桥接。
 */
import { GapTolerantFilter } from '../src/index.ts';

const filter = new GapTolerantFilter({
  b: [0.25],
  a: [1, -0.75],
  gapThreshold: 2,
});

// 正常到达 0–4
for (let i = 0; i <= 4; i++) {
  const cs = filter.addSample(i, Math.sin(i / 2) * 10);
  console.log(cs.summary);
}

// 信号中断后恢复：索引 10–14 到达（缺口 5 > 2 → 断开）
for (let i = 10; i <= 14; i++) {
  const cs = filter.addSample(i, Math.sin(i / 2) * 10);
  if (i === 10) console.log(cs.summary);
}
console.log('\n当前分段:', JSON.stringify(filter.segments()));
console.log('索引 10 的输出（零状态重启）:', filter.getOutput(10));

// 迟到样本补在缺口中间：4→8 缺 3？仍超阈值；再补 6 → 全部拆短
console.log('\n--- 迟到样本到达 ---');
console.log(filter.addSample(8, Math.sin(4) * 10).summary);
console.log(filter.addSample(6, Math.sin(3) * 10).summary);

console.log('\n当前分段:', JSON.stringify(filter.segments()));
console.log('\n索引 10 的版本历史（因补点而改变）:');
for (const e of filter.historyOf(10)) {
  console.log(`  版本 ${e.version}: y = ${e.value.toFixed(6)} (${e.origin})`);
}
console.log('\n最终输出:');
for (const o of filter.getOutputs()) {
  console.log(`  n=${String(o.index).padStart(2)}  y=${o.value.toFixed(6).padStart(10)}  v${o.version}  ${o.origin}`);
}
