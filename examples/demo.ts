import { IncrementalSCC, type SCCDiff } from '../src/incremental-scc.ts';

const g = new IncrementalSCC();

function show(action: string, diffs: SCCDiff[]): void {
  console.log(`\n== ${action} ==`);
  if (diffs.length === 0) {
    console.log('  (无分量变化)');
  }
  for (const d of diffs) {
    switch (d.kind) {
      case 'vertex-added':
        console.log(`  + 顶点 ${d.component.id}，新分量 [${d.component.members}]`);
        break;
      case 'vertex-removed':
        console.log(`  - 顶点 ${d.vertex}（原属分量 ${d.component.id}）`);
        break;
      case 'merge':
        console.log(
          `  ~ 合并：分量 ${d.absorbed.join(', ') || '(无)'} 并入 ${d.component.id}` +
            `，成员 [${d.component.members}]`,
        );
        break;
      case 'split':
        console.log(
          `  ~ 分裂：分量 ${d.from} 拆为 ` +
            d.components.map((c) => `{${c.id}: [${c.members}]}`).join(' + '),
        );
        break;
    }
  }
  console.log(
    '  当前分量：',
    g.components().map((c) => `{${c.id}: [${c.members}]}`).join('  '),
  );
}

// 顶点：A=1, B=2, C=3, D=4
for (const v of [1, 2, 3]) show(`addVertex(${v})`, g.addVertex(v));
show('addEdge(A->B)', g.addEdge(1, 2));
show('addEdge(B->C)', g.addEdge(2, 3));
show('addEdge(C->A)  // 闭合三角环', g.addEdge(3, 1));
show('addVertex(D)', g.addVertex(4));
show('addEdge(C->D)', g.addEdge(3, 4));
show('removeEdge(B->C)  // 环被打破', g.removeEdge(2, 3));
