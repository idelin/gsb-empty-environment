import { toElements } from './elements.ts';
import { layoutOptimal, EPS } from './dp.ts';
import { layoutGreedy } from './greedy.ts';
import { layoutExhaustive } from './exhaustive.ts';
import { layoutWithEdit } from './incremental.ts';
import { formatLayout } from './report.ts';
import { GLYPHS, GREEDY_TRAP, HYPHEN, LONG_WORD, SHORT, TIE, word, type Fixture } from './fixtures.ts';

function showFixture(fixture: Fixture, exhaustiveBudget: number): void {
  const elements = toElements(fixture.paragraph, GLYPHS.hyphenWidth);
  console.log(`\n######## 夹具: ${fixture.name} (行宽 ${fixture.config.lineWidth}, 断词预算 ${fixture.config.hyphenBudget}, 邻行权重 ${fixture.config.adjacencyWeight}) ########`);
  console.log(formatLayout(layoutGreedy(elements, fixture.config), `${fixture.name} / 贪心`, false));
  console.log('');
  const optimal = layoutOptimal(elements, fixture.config);
  console.log(formatLayout(optimal, `${fixture.name} / 动态规划`, true));
  const check = layoutExhaustive(elements, fixture.config, exhaustiveBudget);
  if (check.status === 'ok') {
    const match = Math.abs((check.cost ?? Infinity) - optimal.cost) <= 1e-6;
    console.log(
      `[穷举对照] 访问 ${check.nodesVisited} 节点, 最优成本 ${(check.cost as number).toFixed(4)} — 与 DP ${match ? '一致 ✓' : `不一致 ✗ (DP=${optimal.cost.toFixed(4)})`}`,
    );
  } else {
    console.log(
      `[报告] 预算中断: 穷举访问 ${check.nodesVisited} 节点超过预算 ${check.budget}, 不作最优性结论`,
    );
  }
}

console.log('==== 最优换行排版样例 (节点树与字形度量均由夹具给出) ====');
showFixture(SHORT, 200000);
showFixture(GREEDY_TRAP, 200000);
showFixture(TIE, 200000);
showFixture(HYPHEN, 200000);
showFixture(LONG_WORD, 200000);

console.log('\n######## 预算中断演示 (穷举预算仅 5) ########');
{
  const elements = toElements(SHORT.paragraph, GLYPHS.hyphenWidth);
  const check = layoutExhaustive(elements, SHORT.config, 5);
  console.log(
    check.status === 'budget-exceeded'
      ? `[报告] 预算中断: 访问 ${check.nodesVisited} 节点超过预算 ${check.budget}, 穷举校验不可用, 不声称最优`
      : '[报告] 未触发预算中断',
  );
}

console.log('\n######## 增量编辑演示: 短段落第 6 个节点 "fox" -> "hippopotamus" ########');
{
  const replacement = word('hippopotamus');
  const inc = layoutWithEdit(SHORT.paragraph, 6, replacement, SHORT.config, GLYPHS.hyphenWidth);
  for (const step of inc.certificate) console.log(step);
  console.log(formatLayout(inc.result, '增量编辑后 / 动态规划', true));
}
