import { analyzePattern, runBacktracking } from './index.ts';

const patterns = [
  '(a|aa)*$',
  '^((ab)*)*$',
  'a(b|b)*c',
  'a*$',
  '(a|b)*$',
  '(foo|foot)*$',
];

for (const source of patterns) {
  const report = analyzePattern(source, {
    stepCap: 1_000_000,
    pumpCounts: [0, 4, 8, 12, 16],
  });

  console.log(`\n模式 ${source} => ${report.status}`);
  if (report.witness) {
    console.log(`  前缀:       ${JSON.stringify(report.witness.prefix)}`);
    console.log(`  重复段:     ${JSON.stringify(report.witness.pump)}`);
    console.log(`  失败后缀:   ${JSON.stringify(report.witness.failingSuffix)}`);
    console.log(`  死字符:     ${JSON.stringify(report.witness.deadChar)}`);
  }
  for (const sample of report.growth) {
    const ratio =
      sample.ratioToPrevious === null ? '  -  ' : sample.ratioToPrevious.toFixed(2);
    console.log(
      `  k=${String(sample.pumpCount).padStart(2)} 步数=${String(sample.steps).padStart(7)} ` +
        `撞顶=${sample.capped ? '是' : '否'} 比值=${ratio}`,
    );
  }
  console.log(`  说明: ${report.reason}`);
}

// 教学回溯解释器单独演示：步数上限如何把指数回溯变成可观察、可截断的现象。
const demoPattern = '(a|aa)*$';
console.log(`\n教学解释器（${demoPattern}，步数上限 5000）`);
for (const length of [8, 12, 16, 20]) {
  const result = runBacktracking(demoPattern, 'a'.repeat(length) + '~', {
    stepCap: 5000,
    maxTraceEvents: 5,
  });
  console.log(
    `  输入长度 ${String(length).padStart(2)}: 步数=${String(result.steps).padStart(5)} ` +
      `撞顶=${result.capped ? '是' : '否'} 轨迹事件=${result.trace.length}`,
  );
}

console.log(
  '\n注意：no-evidence 只表示未找到“可泵送歧义 + 失败后缀”的证据，绝不等于正则安全。',
);
