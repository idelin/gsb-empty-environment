import { DynamicTransitiveClosure } from '../src/index.ts';

const tc = new DynamicTransitiveClosure<string>();

console.log('— 批量加边 —');
const d1 = tc.addEdges([
  ['北京', '上海'],
  ['上海', '杭州'],
  ['北京', '杭州'], // 直达
  ['杭州', '深圳'],
]);
console.log('新增可达对:', d1.added.map((e) => `${e.from}->${e.to}`));

console.log('\n— 删除直达边 北京->杭州 —');
const d2 = tc.removeEdges([['北京', '杭州']]);
console.log('消失的可达对:', d2.removed); // 空：北京->上海->杭州 仍然支撑
console.log('北京->杭州 见证路径:', tc.witnessPath('北京', '杭州'));

console.log('\n— 删除 上海->杭州 —');
const d3 = tc.removeEdges([['上海', '杭州']]);
console.log('消失的可达对:', d3.removed.map((e) => `${e.from}->${e.to}`));

console.log('\n与全量搜索对照:', tc.verify().ok ? '一致 ✓' : '不一致 ✗');
