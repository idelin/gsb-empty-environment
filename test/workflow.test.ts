import { describe, expect, test } from 'bun:test';
import {
  Workflow,
  branch,
  task,
  when,
  type Emitted,
  type EngineEvent,
} from '../src/workflow';

const complete = (taskId: string): EngineEvent => ({ type: 'task.completed', taskId });
const cancel = (nodeId: string): EngineEvent => ({ type: 'cancel', nodeId });

const ofType = <T extends Emitted['type']>(emitted: readonly Emitted[], type: T) =>
  emitted.filter((e) => e.type === type);

const joins = (emitted: readonly Emitted[], branchId: string) =>
  ofType(emitted, 'branch.joined').filter((e) => e.type === 'branch.joined' && e.branchId === branchId);

const startedIds = (emitted: readonly Emitted[]) =>
  ofType(emitted, 'task.started').map((e) => (e.type === 'task.started' ? e.taskId : ''));

function permutations<T>(items: readonly T[]): T[][] {
  if (items.length <= 1) return [items.slice()];
  const out: T[][] = [];
  for (let i = 0; i < items.length; i++) {
    const rest = items.slice(0, i).concat(items.slice(i + 1));
    for (const tail of permutations(rest)) out.push([items[i]!, ...tail]);
  }
  return out;
}

function uniquePermutations<T>(items: readonly T[], key: (item: T) => string): T[][] {
  const seen = new Set<string>();
  return permutations(items).filter((perm) => {
    const k = perm.map(key).join('|');
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

describe('分支选择', () => {
  test('仅启动条件满足的子任务，未选分支保持 pending', () => {
    const wf = new Workflow(
      branch('root', [
        when(task('a')),
        when(task<{ useB: boolean; useC: boolean }>('b'), (c) => c.useB),
        when(task<{ useB: boolean; useC: boolean }>('c'), (c) => c.useC),
      ]),
      { useB: true, useC: false },
    );
    expect(startedIds(wf.emitted)).toEqual(['a', 'b']);
    expect(wf.nodeStatus('c')).toBe('pending');
    expect(wf.selectedOf('root')).toEqual(['a', 'b']);
    const selected = ofType(wf.emitted, 'branch.selected');
    expect(selected).toHaveLength(1);
  });

  test('汇合只等待实际启动的集合，不等待未选分支', () => {
    const wf = new Workflow(
      branch('root', [when(task('a')), when(task('b'), () => false)]),
      {},
    );
    wf.dispatch(complete('a'));
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    expect(wf.status).toBe('completed');
    expect(wf.nodeStatus('b')).toBe('pending');
  });

  test('空选择立即汇合一次', () => {
    const wf = new Workflow(branch('root', [when(task('a'), () => false)]), {});
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    expect(wf.status).toBe('completed');
    expect(wf.done).toBe(true);
  });

  test('选中集合在分支选择时冻结，之后 ctx 变化不影响', () => {
    const ctx = { flag: true };
    const wf = new Workflow(
      branch('root', [
        when(task<typeof ctx>('x'), (c) => c.flag),
        when(task<typeof ctx>('y'), (c) => !c.flag),
      ]),
      ctx,
    );
    ctx.flag = false; // 选择之后翻转条件
    wf.dispatch(complete('x'));
    expect(startedIds(wf.emitted)).toEqual(['x']);
    expect(wf.selectedOf('root')).toEqual(['x']);
    expect(wf.nodeStatus('y')).toBe('pending');
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    expect(wf.status).toBe('completed');
  });
});

describe('完成事件', () => {
  test('重复完成事件被忽略', () => {
    const wf = new Workflow(branch('root', [when(task('a')), when(task('b'))]), {});
    const first = wf.dispatch(complete('a'));
    const dup = wf.dispatch(complete('a'));
    expect(dup).toEqual([]); // 第二次完成不产生任何效果
    expect(wf.evidence).toEqual([]);
    wf.dispatch(complete('b'));
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    expect(first.some((e) => e.type === 'branch.joined')).toBe(false);
  });

  test('未启动任务的完成事件被忽略', () => {
    const wf = new Workflow(branch('root', [when(task('a')), when(task('b'), () => false)]), {});
    expect(wf.dispatch(complete('b'))).toEqual([]);
    expect(wf.nodeStatus('b')).toBe('pending');
    expect(wf.dispatch(complete('ghost'))).toEqual([]);
  });
});

describe('取消与迟到成功', () => {
  test('取消后迟到成功只记证据，不改变状态、不推进流程', () => {
    const wf = new Workflow(branch('root', [when(task('a')), when(task('b'))]), {});
    wf.dispatch(complete('a'));
    wf.dispatch(cancel('b'));
    // b 已终态，汇合因 a、b 均终态而触发一次，结果为 cancelled
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    const late = wf.dispatch(complete('b'));
    expect(late).toEqual([{ type: 'evidence.recorded', taskId: 'b', kind: 'late-success' }]);
    expect(wf.evidence).toEqual([{ kind: 'late-success', taskId: 'b', seq: 3 }]);
    expect(wf.nodeStatus('b')).toBe('cancelled');
    expect(joins(wf.emitted, 'root')).toHaveLength(1); // 没有第二次汇合
    expect(wf.status).toBe('cancelled'); // 迟到成功没有把流程翻成 completed
  });

  test('兄弟仍在运行时，迟到成功不推进汇合', () => {
    const wf = new Workflow(branch('root', [when(task('a')), when(task('b'))]), {});
    wf.dispatch(cancel('b'));
    wf.dispatch(complete('b')); // 迟到成功：此时 a 未完成，汇合不得触发
    expect(joins(wf.emitted, 'root')).toHaveLength(0);
    expect(wf.evidence).toHaveLength(1);
    wf.dispatch(complete('a'));
    expect(joins(wf.emitted, 'root')).toHaveLength(1);
    expect(wf.status).toBe('cancelled');
  });

  test('取消不覆盖已完成的节点', () => {
    const wf = new Workflow(branch('root', [when(task('a')), when(task('b'))]), {});
    wf.dispatch(complete('a'));
    wf.dispatch(cancel('root'));
    expect(wf.nodeStatus('a')).toBe('completed');
    expect(wf.nodeStatus('b')).toBe('cancelled');
    expect(wf.status).toBe('cancelled');
  });
});

describe('事件排列：汇合恰好一次', () => {
  const def3 = () => branch('root', [when(task('a')), when(task('b')), when(task('c'))]);

  test('3 个完成事件的全部排列：每次运行汇合恰好一次', () => {
    const perms = permutations(['a', 'b', 'c'].map(complete));
    expect(perms).toHaveLength(6);
    for (const perm of perms) {
      const wf = new Workflow(def3(), {});
      for (const event of perm) wf.dispatch(event);
      expect(joins(wf.emitted, 'root')).toHaveLength(1);
      expect(wf.status).toBe('completed');
      expect(wf.evidence).toEqual([]);
    }
  });

  test('含重复完成的事件多重集全部排列：汇合仍恰好一次', () => {
    const events = ['a', 'a', 'b', 'b', 'c', 'c'].map(complete);
    const perms = uniquePermutations(events, (e) => (e.type === 'task.completed' ? e.taskId : ''));
    expect(perms.length).toBeGreaterThan(1);
    for (const perm of perms) {
      const wf = new Workflow(def3(), {});
      for (const event of perm) wf.dispatch(event);
      expect(joins(wf.emitted, 'root')).toHaveLength(1);
      expect(wf.status).toBe('completed');
      expect(wf.evidence).toEqual([]);
    }
  });

  test('取消与迟到成功交错的全排列：不变量在所有顺序下成立', () => {
    const events: EngineEvent[] = [complete('a'), complete('b'), cancel('b')];
    for (const perm of permutations(events)) {
      const wf = new Workflow(branch('root', [when(task('a')), when(task('b'))]), {});
      for (const event of perm) wf.dispatch(event);
      const cancelIndex = perm.findIndex((e) => e.type === 'cancel');
      const completeBIndex = perm.findIndex((e) => e.type === 'task.completed' && e.taskId === 'b');
      expect(joins(wf.emitted, 'root')).toHaveLength(1);
      expect(wf.done).toBe(true);
      if (cancelIndex < completeBIndex) {
        // 先取消后完成：迟到成功只记证据
        expect(wf.evidence).toEqual([{ kind: 'late-success', taskId: 'b', seq: completeBIndex + 1 }]);
        expect(wf.nodeStatus('b')).toBe('cancelled');
        expect(wf.status).toBe('cancelled');
      } else {
        // 先完成后取消：取消为空操作
        expect(wf.evidence).toEqual([]);
        expect(wf.nodeStatus('b')).toBe('completed');
        expect(wf.status).toBe('completed');
      }
    }
  });
});

describe('嵌套分支', () => {
  const nestedDef = () =>
    branch('outer', [
      when(task('x')),
      when(
        branch('inner', [
          when(task('p')),
          when(task('q'), () => false),
          when(task('r')),
        ]),
      ),
    ]);

  test('内层冻结自己的集合，外层汇合等待内层汇合', () => {
    const wf = new Workflow(nestedDef(), {});
    expect(startedIds(wf.emitted).sort()).toEqual(['p', 'r', 'x']);
    expect(wf.selectedOf('inner')).toEqual(['p', 'r']);
    expect(wf.selectedOf('outer')).toEqual(['x', 'inner']);
    expect(wf.nodeStatus('q')).toBe('pending');
    wf.dispatch(complete('p'));
    wf.dispatch(complete('r'));
    expect(joins(wf.emitted, 'inner')).toHaveLength(1);
    expect(joins(wf.emitted, 'outer')).toHaveLength(0); // x 未完成，外层不汇合
    wf.dispatch(complete('x'));
    expect(joins(wf.emitted, 'outer')).toHaveLength(1);
    expect(wf.status).toBe('completed');
  });

  test('嵌套分支下完成事件全排列：每层汇合恰好一次且内层先于外层', () => {
    for (const perm of permutations(['x', 'p', 'r'].map(complete))) {
      const wf = new Workflow(nestedDef(), {});
      for (const event of perm) wf.dispatch(event);
      const innerJoins = joins(wf.emitted, 'inner');
      const outerJoins = joins(wf.emitted, 'outer');
      expect(innerJoins).toHaveLength(1);
      expect(outerJoins).toHaveLength(1);
      const innerAt = wf.emitted.indexOf(innerJoins[0]!);
      const outerAt = wf.emitted.indexOf(outerJoins[0]!);
      expect(innerAt).toBeLessThan(outerAt);
      expect(startedIds(wf.emitted).sort()).toEqual(['p', 'r', 'x']);
      expect(wf.status).toBe('completed');
    }
  });

  test('取消内层分支后，迟到成功只记证据，外层汇合结果反映取消', () => {
    const wf = new Workflow(nestedDef(), {});
    wf.dispatch(complete('p'));
    wf.dispatch(cancel('inner'));
    expect(joins(wf.emitted, 'inner')).toHaveLength(0); // 内层被取消，不发生汇合
    expect(wf.nodeStatus('p')).toBe('completed'); // 已完成的不被覆盖
    expect(wf.nodeStatus('r')).toBe('cancelled');
    wf.dispatch(complete('x'));
    expect(joins(wf.emitted, 'outer')).toHaveLength(1);
    expect(wf.status).toBe('cancelled');
    const late = wf.dispatch(complete('r')); // 迟到成功
    expect(late).toEqual([{ type: 'evidence.recorded', taskId: 'r', kind: 'late-success' }]);
    expect(wf.evidence).toEqual([{ kind: 'late-success', taskId: 'r', seq: 4 }]);
    expect(joins(wf.emitted, 'outer')).toHaveLength(1);
    expect(wf.status).toBe('cancelled');
  });
});
