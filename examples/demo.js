import { analyze } from '../src/analyzer.js';
import { checkPrecision } from '../src/precision.js';
import {
  lit,
  obj,
  union,
  required,
  optional,
  enumerate,
  typeToString,
} from '../src/types.js';
import { get, val, eq, iff, leaf } from '../src/ast.js';

const Event = union(
  obj({ tag: required(lit('click')), x: required(lit(0)) }),
  obj({ tag: optional(union(lit('scroll'), lit('hover'))), y: required(lit(0)) }),
  obj({ name: required(lit('anon')) }),
);

const program = iff(
  eq(get('tag'), val('scroll')),
  leaf('scroll'),
  iff(eq(get('tag'), val('click')), leaf('click'), leaf('other')),
);

console.log('输入类型:', typeToString(Event));
console.log('值集合:', JSON.stringify(enumerate(Event)));
console.log();

const staticResult = analyze(Event, program);
console.log('== 静态分析 ==');
for (const l of staticResult.leaves) {
  console.log(`叶子 ${l.label}: ${l.reachable ? typeToString(l.type) : 'never(不可达)'}`);
}
console.log('合流剩余类型:', typeToString(staticResult.residual));
console.log('诊断:', staticResult.diagnostics.length ? staticResult.diagnostics : '(无)');
console.log('不可达分支:', staticResult.unreachable.length ? staticResult.unreachable : '(无)');
console.log();

const report = checkPrecision(Event, program, enumerate(Event));
console.log('== 精度核对 ==');
for (const l of report.leaves) {
  console.log(`叶子 ${l.label}: ${l.status}`);
  if (l.missing.length) console.log(`  missing(动态有静态无): ${l.missing}`);
  if (l.spurious.length) console.log(`  spurious(静态有动态无): ${l.spurious}`);
}
console.log('总体:', report.pass ? '精确' : '不精确');
