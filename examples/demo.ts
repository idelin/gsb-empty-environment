/** Runnable end-to-end sample: mouse/keyboard parity, cancel, two racing
 *  drag sessions, idempotent retry, failure preserving independent edits,
 *  and safe termination when the drop target disappears mid-drag. */
import { Server } from '../src/server.ts';
import { ListClient } from '../src/client.ts';
import { KeyboardDriver } from '../src/input/keyboard.ts';
import { MouseDriver } from '../src/input/mouse.ts';
import { pointForLocation } from '../src/layout.ts';
import { formatTree } from '../src/tree.ts';
import { makeTree, METRICS } from '../src/fixtures.ts';
import type { CommitOp, TraceEntry } from '../src/types.ts';

const show = (t: string) => console.log(`\n=== ${t} ===`);
const fmtTrace = (trace: TraceEntry[]) =>
  trace.map((e) => (e.op === 'commit' ? `commit ${e.patch.nodeId} -> ${e.patch.to.parentId}[${e.patch.to.index}]`
    : e.op === 'target' ? `target ${e.parentId}[${e.index}]`
    : e.op === 'terminate' ? `terminate(${e.reason})`
    : `${e.op}${e.op === 'start' ? ' ' + e.nodeId : ''}`)).join(' | ');

// ---------------------------------------------------------------- 1. keyboard
show('1. Keyboard drag: lift "plantain", ArrowLeft promotes it a level');
{
  const server = new Server(makeTree());
  const client = new ListClient(server, 'kbd');
  const kb = new KeyboardDriver(client);
  for (let i = 0; i < 4; i++) kb.key('ArrowDown'); // focus plantain
  kb.key('Enter');       // lift
  kb.key('ArrowLeft');   // promote next to its parent "banana"
  kb.key('Enter');       // commit
  console.log('trace:', fmtTrace(client.machine.trace));
  console.log(formatTree(client.tree));
  console.log('ui:', JSON.stringify(client.machine.ui));
}

// ------------------------------------------------- 2. mouse produces the same
show('2. Mouse drag to the same spot: identical trace and patch');
{
  const server = new Server(makeTree());
  const client = new ListClient(server, 'mouse');
  const md = new MouseDriver(client, METRICS);
  md.pointerDown(33, 70); // grab "plantain"
  const p = pointForLocation(client.tree, METRICS, { parentId: 'fruits', index: 2 }, 'plantain');
  md.pointerMove(p.x, p.y);
  md.pointerUp();
  console.log('trace:', fmtTrace(client.machine.trace));
  const patch = client.machine.trace.find((e) => e.op === 'commit');
  console.log('patch:', JSON.stringify(patch));
  console.log('ui:', JSON.stringify(client.machine.ui));
}

// ------------------------------------------------------------------ 3. cancel
show('3. Cancel restores focus/selection and never touches the list');
{
  const server = new Server(makeTree());
  const client = new ListClient(server, 'kbd');
  client.machine.ui = { focusId: 'apple', selection: ['apple', 'cherry'] };
  const kb = new KeyboardDriver(client);
  kb.key('Enter'); kb.key('ArrowDown'); kb.key('Escape');
  console.log('trace:', fmtTrace(client.machine.trace));
  console.log('list unchanged:', formatTree(client.tree) === formatTree(makeTree()));
  console.log('ui restored:', JSON.stringify(client.machine.ui));
  console.log('server version:', server.version);
}

// ----------------------------------------------------- 4. two sessions racing
show('4. Two drag sessions race; the loser keeps the server state');
{
  const server = new Server(makeTree());
  const a = new ListClient(server, 'alice');
  const b = new ListClient(server, 'bob');
  a.startDrag('apple'); a.dragTo({ parentId: 'grains', index: 1 });
  b.startDrag('carrot'); b.dragTo({ parentId: 'fruits', index: 0 });
  console.log('alice commit:', JSON.stringify(a.commitDrag()));
  console.log('bob commit:  ', JSON.stringify(b.commitDrag()));
  console.log('bob converged:', formatTree(b.tree) === formatTree(server.tree));
  console.log(formatTree(server.tree));
}

// ---------------------------------------------------- 5. idempotent retry
show('5. Lost acknowledgement + retry: applied exactly once');
{
  const server = new Server(makeTree());
  const client = new ListClient(server, 'retry');
  let dropAck = true;
  let opId = '';
  client.transport = (op: CommitOp) => {
    opId = op.opId;
    const res = server.commit('retry', op);
    return dropAck ? null : res;
  };
  client.startDrag('cherry'); client.dragTo({ parentId: 'veggies', index: 2 });
  console.log('first attempt ack:', client.commitDrag(), '(phase:', client.machine.phase + ')');
  dropAck = false;
  client.retryPending();
  console.log('after retry, server version:', server.version, '| op:', opId);
  console.log('veggies children:', JSON.stringify(client.tree.nodes.veggies.children));
}

// --------------------------------- 6. failed commit keeps independent edits
show('6. Stale commit fails; the independent edit made meanwhile survives');
{
  const server = new Server(makeTree());
  const client = new ListClient(server, 'editor');
  client.startDrag('banana');
  client.insertNode('fruits', 1, { id: 'apricot', parentId: null, label: 'Apricot', children: [] });
  client.dragTo({ parentId: 'grains', index: 0 });
  console.log('commit:', JSON.stringify(client.commitDrag()));
  console.log('apricot preserved:', client.tree.nodes.apricot !== undefined);
  console.log('banana unmoved:', client.tree.nodes.banana.parentId === 'fruits');
}

// ------------------------------------ 7. target deleted while dragging
show('7. Drop target deleted mid-drag: safe termination');
{
  const server = new Server(makeTree());
  const a = new ListClient(server, 'alice');
  const b = new ListClient(server, 'bob');
  a.startDrag('apple'); a.dragTo({ parentId: 'veggies', index: 1 });
  b.removeNode('veggies');
  console.log('trace:', fmtTrace(a.machine.trace));
  console.log('phase:', a.machine.phase, '| commit now returns:', a.commitDrag());
  console.log('apple still under fruits:', a.tree.nodes.apple.parentId === 'fruits');
}
