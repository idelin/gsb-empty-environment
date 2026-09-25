import { ClientController } from './controller.ts';
import { fixtureMetrics, fixtureTree } from './fixtures.ts';
import { KeyboardAdapter } from './keyboard.ts';
import { MouseAdapter } from './mouse.ts';
import { Server } from './server.ts';
import { listRows } from './tree.ts';
import type { ApplyResult, MoveOp, TraceEntry, TreeState } from './types.ts';

function renderTree(tree: TreeState): string {
  return listRows(tree)
    .map((r) => `${'  '.repeat(r.depth)}- ${tree.nodes[r.id].label} (${r.id})`)
    .join('\n');
}

function renderTrace(trace: TraceEntry[]): string {
  return trace
    .map((e) => {
      switch (e.kind) {
        case 'begin': return `begin(${e.sourceId})`;
        case 'preview': return `preview(${e.spec.sourceId} ${e.spec.placement} ${e.spec.targetId})`;
        case 'commit': return `commit(${e.op.opId}: ${e.op.sourceId} ${e.op.placement} ${e.op.targetId}, srcV=${e.op.sourceVersion}, tgtR=${e.op.targetRevision})`;
        case 'cancel': return 'cancel';
        case 'terminate': return `terminate(${e.reason})`;
      }
    })
    .join(' -> ');
}

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

console.log('可排序层级列表：鼠标/键盘共用状态机演示');
console.log('初始树 (v0):');
console.log(renderTree(fixtureTree()));

// [1] 鼠标拖拽：a2 -> inside b
section('[1] 鼠标拖拽 a2 -> inside b（预览与提交分离）');
{
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(c, fixtureMetrics);
  const canonicalRef = c.tree;
  mouse.pointerDown(8, 60);
  mouse.pointerMove(8, 78);
  mouse.pointerMove(20, 90);
  console.log('拖动中正式树未被修改:', c.tree === canonicalRef);
  console.log('预览行:', c.previewRows().map((r) => `${r.id}@${r.depth}`).join(' '));
  mouse.pointerUp();
  console.log('轨迹:', renderTrace(c.trace));
  console.log(`提交后树 (v${c.tree.version}):`);
  console.log(renderTree(c.tree));
  console.log('焦点:', c.focusId, '选择:', [...c.selection].join(','));
}

// [2] 键盘执行同一移动：轨迹一致
section('[2] 键盘执行同一移动：逻辑操作轨迹与鼠标完全一致');
{
  const mouseC = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(mouseC, fixtureMetrics);
  mouse.pointerDown(8, 60);
  mouse.pointerMove(8, 78);
  mouse.pointerMove(20, 90);
  mouse.pointerUp();

  const kbC = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const kb = new KeyboardAdapter(kbC);
  kbC.setFocus('a2');
  kb.key(' ');
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Enter');

  console.log('鼠标轨迹:', renderTrace(mouseC.trace));
  console.log('键盘轨迹:', renderTrace(kbC.trace));
  console.log('轨迹一致:', JSON.stringify(mouseC.trace) === JSON.stringify(kbC.trace));
  console.log('最终树一致:', JSON.stringify(mouseC.tree) === JSON.stringify(kbC.tree));
  console.log('焦点/选择一致:', mouseC.focusId === kbC.focusId
    && JSON.stringify([...mouseC.selection]) === JSON.stringify([...kbC.selection]));
}

// [3] 键盘 [ 提升层级 vs 鼠标拖到同位置
section('[3] 键盘 [ 提升层级 与 鼠标拖到同位置 -> 同一补丁');
{
  const kbC = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const kb = new KeyboardAdapter(kbC);
  kbC.setFocus('a1');
  kb.key(' ');
  kb.key('[');
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Enter');

  const mouseC = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(mouseC, fixtureMetrics);
  mouse.pointerDown(8, 36);
  mouse.pointerMove(8, 66);
  mouse.pointerUp();

  const patch = (c: ClientController) => c.trace.find((e) => e.kind === 'commit');
  console.log('键盘补丁:', JSON.stringify(patch(kbC)));
  console.log('鼠标补丁:', JSON.stringify(patch(mouseC)));
  console.log('补丁一致:', JSON.stringify(patch(kbC)) === JSON.stringify(patch(mouseC)));
  console.log('提升后根节点顺序:', kbC.tree.rootIds.join(' '));
}

// [4] 取消恢复全部状态
section('[4] 取消：列表不变，焦点/选择全部恢复');
{
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  c.setFocus('a2');
  c.select(['a', 'b1']);
  const kb = new KeyboardAdapter(c);
  kb.key(' ');
  kb.key('ArrowDown');
  kb.key('ArrowDown');
  kb.key('Escape');
  console.log('轨迹:', renderTrace(c.trace));
  console.log('树未变:', JSON.stringify(c.tree) === JSON.stringify(fixtureTree()));
  console.log('焦点恢复:', c.focusId === 'a2', '选择恢复:', [...c.selection].sort().join(',') === 'a,b1');
}

// [5] 双会话竞争 + 独立编辑保留
section('[5] 两个拖拽会话竞争：先提交者胜，独立编辑保留');
{
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  const kb = new KeyboardAdapter(c);
  mouse.pointerDown(8, 36);   // M: a1 -> inside b
  mouse.pointerMove(20, 90);
  c.setFocus('c');            // K: c -> before a
  kb.key(' ');
  for (let i = 0; i < 5; i++) kb.key('ArrowUp');
  console.log('进行中会话数:', c.activeSessionCount());
  mouse.pointerUp();          // M 提交成功 (v1)
  c.rename('a2', 'A2 renamed'); // 独立编辑 (v2)
  kb.key('Enter');            // K 提交：源版本过期被拒
  console.log('M 提交后 a1 已在 b 内:', JSON.stringify(server.tree.nodes.b.childIds) === '["b1","a1"]');
  console.log('K 被拒，c 未被移动:', JSON.stringify(server.tree.rootIds) === '["a","b","c"]');
  console.log('独立编辑保留:', server.tree.nodes.a2.label === 'A2 renamed');
  console.log('轨迹:', renderTrace(c.trace));
}

// [6] 幂等重试
section('[6] 操作ID幂等：响应丢失，同一 opId 重试只应用一次');
{
  const server = new Server(fixtureTree());
  let calls = 0;
  const flaky = (op: MoveOp): ApplyResult | null => {
    calls += 1;
    const result = server.applyMove(op);
    return calls === 1 ? null : result;
  };
  const c = new ClientController(fixtureTree(), server, flaky);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 60);
  mouse.pointerMove(20, 90);
  mouse.pointerUp();
  console.log('传输调用次数:', calls, '(首次响应丢失后重试)');
  console.log('服务器应用次数:', server.appliedOpCount(), '树版本:', server.tree.version);
  console.log('a2 只移动一次:', JSON.stringify(c.tree.nodes.b.childIds) === '["b1","a2"]');
}

// [7] 目标在拖动中被删除
section('[7] 目标在拖动中被删除：会话安全终止');
{
  const server = new Server(fixtureTree());
  const c = new ClientController(fixtureTree(), server);
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 36);
  mouse.pointerMove(20, 90);  // 预览 inside b
  c.remove('b');              // 服务器删除 b 并回包
  mouse.pointerUp();          // 安全空操作
  console.log('轨迹:', renderTrace(c.trace));
  console.log('剩余行:', c.rows().map((r) => r.id).join(' '));
  console.log('无 commit 产生:', !c.trace.some((e) => e.kind === 'commit'));
}

// [8] 禁止移入自身后代
section('[8] 禁止移入自身后代');
{
  const c = new ClientController(fixtureTree(), new Server(fixtureTree()));
  const mouse = new MouseAdapter(c, fixtureMetrics);
  mouse.pointerDown(8, 12);   // 按在 a
  mouse.pointerMove(40, 60);  // 试图移入 a1（a 的后代）
  mouse.pointerUp();
  console.log('轨迹:', renderTrace(c.trace));
  console.log('树未变:', JSON.stringify(c.tree) === JSON.stringify(fixtureTree()));
}
