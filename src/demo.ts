/**
 * 演示：一组各自适用的修复建议，组合时因重叠/前置/语义冲突不能全选。
 * 运行：npm run demo
 */
import { combineFixes } from './combine';
import { symbolDefined, textContains } from './preconditions';
import { FixSuggestion } from './types';

const source = [
  'export function calcTotal(items) {',   // 0..31
  '  let total = 0;',                      // 32..49
  '  for (const it of items) {',           // 50..80
  '    total += it.price;',                // 81..103
  '  }',                                   // 104..107
  '  return total;',                       // 108..123
  '}',                                     // 124..126
  '',
].join('\n');

const suggestions: FixSuggestion[] = [
  {
    id: 'A',
    description: '把 total 改名为 sum（区间改写整段函数体）',
    benefit: 8,
    edits: [{ file: 'a.ts', start: 35, end: 51, replacement: '  let sum = 0;' }],
    writesSymbols: ['total'],
  },
  {
    id: 'B',
    description: '给 total 初始化加类型注解（与 A 改写同一区间）',
    benefit: 5,
    edits: [{ file: 'a.ts', start: 41, end: 50, replacement: 'total: number = 0' }],
    writesSymbols: ['total'],
  },
  {
    id: 'C',
    description: '把 return total 改为 return sum（依赖 A 先改名）',
    benefit: 6,
    requires: ['A'],
    edits: [{ file: 'a.ts', start: 107, end: 122, replacement: '  return sum;' }],
    readsSymbols: ['total'],
    preconditions: [textContains('a.ts', 'return total', '仍存在 return total 语句')],
  },
  {
    id: 'D',
    description: '提取 price 常量（文本不重叠，但同样改写符号 total）',
    benefit: 4,
    edits: [{ file: 'a.ts', start: 80, end: 102, replacement: '    total += it.price; // checked' }],
    writesSymbols: ['total'],
  },
  {
    id: 'E',
    description: '删除 calcTotal 函数（会使依赖该函数的修复失效）',
    benefit: 3,
    edits: [{ file: 'a.ts', start: 0, end: 124, replacement: '' }],
    invalidates: ['C'],
    preconditions: [symbolDefined('a.ts', 'calcTotal')],
  },
];

const result = combineFixes({ 'a.ts': source }, suggestions);

console.log('=== 选中的修复（按应用顺序）===');
for (const s of result.selected) {
  console.log(`  #${s.order} [${s.id}] ${s.description}（收益 ${s.benefit}）`);
}
console.log(`总收益: ${result.totalBenefit}（精确求解: ${result.exact}）`);

console.log('\n=== 未选中建议及证据 ===');
for (const r of result.rejected) {
  console.log(`  [${r.id}] ${r.description}`);
  for (const reason of r.reasons) {
    switch (reason.kind) {
      case 'conflict':
        for (const c of reason.conflicts) console.log(`    - 冲突(${c.kind}) 与 ${c.otherId}: ${c.detail}`);
        break;
      case 'post-apply-precondition-failed':
        console.log(`    - 应用后前置条件失效: ${reason.precondition}（被 ${reason.invalidatedBy.join(', ') || '未知'} 破坏）`);
        break;
      default:
        console.log(`    - ${reason.kind}: ${JSON.stringify(reason)}`);
    }
  }
}

console.log('\n=== 应用后的文件 ===');
console.log(result.finalFiles['a.ts']);
