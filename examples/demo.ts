/**
 * 运行样例：验收边界场景
 *   用户 A 输入 'hello'，用户 B 插入 '!'；
 *   A 执行 Undo 撤回 'hello'，文档保留 '!'；
 *   A 执行 Redo，无缝恢复为 'hello!'。
 */
import { Replica } from '../src/replica.ts';
import type { OpNode } from '../src/types.ts';

function sync(from: Replica, to: Replica): void {
  for (const node of from.drainOutbox()) to.receive(node);
}

function show(step: string, A: Replica, B: Replica): void {
  console.log(`${step}`);
  console.log(`  A 视图: "${A.doc.text()}"   B 视图: "${B.doc.text()}"`);
}

const A = new Replica('A');
const B = new Replica('B');

A.insertText(null, 'hello');
sync(A, B);
show('1) A 输入 hello 并同步', A, B);

B.insertText(B.doc.lastVisibleCharId(), '!');
sync(B, A);
show('2) B 在末尾插入 ! 并同步', A, B);

const undoNode = A.undo() as OpNode;
sync(A, B);
show('3) A 执行 Undo（追加补偿节点 ' + undoNode.id + '）', A, B);

const redoNode = A.redo() as OpNode;
sync(A, B);
show('4) A 执行 Redo（追加补偿节点 ' + redoNode.id + '）', A, B);

console.log('\nDAG 节点（拓扑序，parents 为因果依赖）:');
for (const node of A.nodes.values()) {
  const comp = node.compensates ? `  compensates=${node.compensates}` : '';
  console.log(
    `  ${node.id}  ${node.author}:${node.kind}  parents=[${node.parents.join(', ')}]${comp}`,
  );
}
