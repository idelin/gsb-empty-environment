import { solveSeam, type SeamProblem } from '../src/seam.ts';

// 4×3 重叠区：A 在左侧便宜，B 在右侧便宜，切换有代价，底边固定为 B
const problem: SeamProblem = {
  width: 4,
  height: 3,
  costA: [
    [1, 2, 8, 9],
    [1, 2, 8, 9],
    [1, 2, 8, 9],
  ],
  costB: [
    [9, 8, 2, 1],
    [9, 8, 2, 1],
    [9, 8, 2, 1],
  ],
  switchCostHorizontal: [
    [3, 3, 3],
    [3, 3, 3],
    [3, 3, 3],
  ],
  switchCostVertical: [
    [3, 3, 3, 3],
    [3, 3, 3, 3],
  ],
  fixed: [{ x: 0, y: 2, label: 1 }], // 左下角强制取 B
};

const r = solveSeam(problem, { tieBreak: 'A' });
console.log('分割（A/B）:');
for (const row of r.labels) console.log('  ' + row.map((l) => (l === 0 ? 'A' : 'B')).join(' '));
console.log('费用明细:');
console.log(`  来源代价   A=${r.sourceCostA}  B=${r.sourceCostB}  小计=${r.sourceCost}`);
console.log(`  切换代价   水平=${r.switchCostHorizontal}  垂直=${r.switchCostVertical}  小计=${r.switchCost}`);
console.log(`  总费用     ${r.totalCost}`);
