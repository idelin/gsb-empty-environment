import { BranchingHistory } from '../src/history.ts';

const history = new BranchingHistory();
const show = (label: string) => {
  const state = history.getSnapshot();
  console.log(
    `${label}: text=${JSON.stringify(state.text)}, anchor=${state.selection.anchor}, active=${state.selection.active}, node=${history.currentNodeId}`,
  );
};

history.replace(0, 0, 'a', { anchor: 1, active: 1 });
history.replace(1, 1, 'b', { anchor: 2, active: 2 });
show('typed ab');

history.undo();
show('undo    ');

history.replace(0, 0, 'x', { anchor: 1, active: 0 });
show('typed x ');

console.log('branches:', history.getBranches().map((branch) => ({
  id: branch.id,
  text: branch.text,
  selection: branch.selection,
})));

const [abBranch, xBranch] = history.getBranches();
history.switchTo(abBranch.id);
show('ab branch');
history.switchTo(xBranch.id);
show('x  branch');

const compact = new BranchingHistory({ maxHistoryNodes: 2 });
compact.replace(0, 0, 'a');
compact.replace(1, 1, 'b');
compact.undo();
compact.replace(0, 0, 'x');
console.log('compact replay:', compact.replayVisibleNodes().map((state) => ({
  node: state.nodeId,
  text: state.text,
})));
