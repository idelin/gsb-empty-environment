import { UndoTree } from '../src/undoTree.ts';

const doc = new UndoTree('r1');
const step = (label: string) => console.log(`${label.padEnd(38)} => 文档: ${JSON.stringify(doc.text())}`);

console.log('=== 验收场景: Undo/Redo CRDT Graph ===\n');

doc.insert('A', null, 'hello');
step('1. 用户 A 输入 "hello"');

doc.insert('B', doc.tailElementId(), '!');
step('2. 用户 B 在末尾插入 "!"');

doc.undo('A');
step('3. 用户 A 执行 Undo (撤回 hello)');

doc.redo('A');
step('4. 用户 A 执行 Redo (恢复 hello)');

console.log('\n=== 操作图 (拓扑序, 虚线节点为反向补偿) ===\n');
console.log(doc.describe());

console.log('\n=== Graphviz DOT ===\n');
console.log(doc.toDot());

console.log('\n=== 双副本收敛演示 ===\n');
const r1 = new UndoTree('r1');
const r2 = new UndoTree('r2');
const aInsert = r1.insert('A', null, 'hello');
r2.receive(aInsert);
const bInsert = r2.insert('B', r2.tailElementId(), '!');
r1.receive(bInsert);
const undoOp = r1.undo('A')!;
r2.receive(undoOp);
console.log(`A 撤销后   r1=${JSON.stringify(r1.text())} r2=${JSON.stringify(r2.text())}`);
const redoOp = r1.redo('A')!;
r2.receive(redoOp);
console.log(`A 重做后   r1=${JSON.stringify(r1.text())} r2=${JSON.stringify(r2.text())}`);
