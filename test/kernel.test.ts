import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TemplateKernel,
  CycleError,
  ReentrancyError,
  InvalidRangeError,
  InvalidatedPlaceholderError,
  FinalizedError,
} from '../src/kernel.ts';

test('主占位改为 xy：两个镜像同时更新且偏移正确', () => {
  //            0123456789
  const k = new TemplateKernel('x - x - x');
  k.addPlaceholder('P', 0, 1);
  k.addMirror('M1', 4, 5, 'P');
  k.addMirror('M2', 8, 9, 'P');

  k.edit('P', 'xy');

  assert.equal(k.text, 'xy - xy - xy');
  assert.equal(k.placeholderText('M1'), 'xy');
  assert.equal(k.placeholderText('M2'), 'xy');
  assert.deepEqual(k.placeholderRange('P'), { start: 0, end: 2 });
  assert.deepEqual(k.placeholderRange('M1'), { start: 5, end: 7 });
  assert.deepEqual(k.placeholderRange('M2'), { start: 10, end: 12 });
});

test('镜像变换长度不同：后续占位不错位', () => {
  //            0123456
  const k = new TemplateKernel('a - a - Z');
  k.addPlaceholder('P', 0, 1);
  // 注册即同步：'a' -> 'aaa'，文本变为 'a - aaa - Z'
  k.addMirror('M', 4, 5, 'P', (s) => s.repeat(3), 'triple');
  k.addPlaceholder('Q', 10, 11); // 基于 M 同步后文本 'a - aaa - Z'
  assert.equal(k.text, 'a - aaa - Z');
  assert.equal(k.placeholderText('Q'), 'Z');

  k.edit('P', 'bc'); // 变长：1 -> 2，镜像 3 -> 6

  assert.equal(k.text, 'bc - bcbcbc - Z');
  assert.equal(k.placeholderText('M'), 'bcbcbc');
  assert.equal(k.placeholderText('Q'), 'Z');
  assert.deepEqual(k.placeholderRange('M'), { start: 5, end: 11 });
  assert.deepEqual(k.placeholderRange('Q'), { start: 14, end: 15 });
});

test('菱形依赖去重：共享节点每次编辑只重算一次', () => {
  //     P
  //    / \
  //   B   C
  //    \ /
  //     D
  // 初始内容已等于变换结果，注册同步为无操作，偏移保持稳定
  //            01234567890123
  const k = new TemplateKernel('p pb pc pb|pc');
  k.addPlaceholder('P', 0, 1);
  const calls = { B: 0, C: 0, D: 0 };
  k.addMirror('B', 2, 4, 'P', (s) => { calls.B++; return s + 'b'; });
  k.addMirror('C', 5, 7, 'P', (s) => { calls.C++; return s + 'c'; });
  k.addMirror('D', 8, 13, ['B', 'C'], (b, c) => { calls.D++; return `${b}|${c}`; });

  assert.deepEqual(calls, { B: 1, C: 1, D: 1 }); // 注册同步各一次
  k.edit('P', 'q');
  assert.deepEqual(calls, { B: 2, C: 2, D: 2 }); // 菱形汇合点 D 只重算一次
  assert.equal(k.text, 'q qb qc qb|qc');
  assert.equal(k.placeholderText('D'), 'qb|qc');
});

test('增量更新：只重算被编辑占位的下游', () => {
  const k = new TemplateKernel('a b a b');
  k.addPlaceholder('P1', 0, 1);
  k.addPlaceholder('P2', 2, 3);
  const calls = { M1: 0, M2: 0 };
  k.addMirror('M1', 4, 5, 'P1', (s) => { calls.M1++; return s.toUpperCase(); });
  k.addMirror('M2', 6, 7, 'P2', (s) => { calls.M2++; return s.toUpperCase(); });
  assert.deepEqual(calls, { M1: 1, M2: 1 });

  k.edit('P1', 'x');
  assert.deepEqual(calls, { M1: 2, M2: 1 }); // M2 未被波及
  assert.equal(k.text, 'x b X B');
});

function capture(fn: () => void): Error {
  try {
    fn();
  } catch (err) {
    return err as Error;
  }
  assert.fail('expected function to throw');
}

test('循环引用在编译期拒绝并报出具体环链', () => {
  const k = new TemplateKernel('. .');
  k.addMirror('A', 0, 1, 'B');
  k.addMirror('B', 2, 3, 'A');

  const err = capture(() => k.compile());
  assert.ok(err instanceof CycleError);
  assert.match(err.message, /A -> B -> A/);
  assert.deepEqual((err as CycleError).chain, ['A', 'B', 'A']);

  // 编辑同样被拒绝（edit 内部先编译）
  assert.throws(() => k.edit('A', 'z'), /A -> B -> A/);
});

test('自引用与长环', () => {
  const k = new TemplateKernel('. . .');
  k.addMirror('A', 0, 1, 'C');
  k.addMirror('B', 2, 3, 'A');
  k.addMirror('C', 4, 5, 'B');
  const err = capture(() => k.compile());
  assert.ok(err instanceof CycleError);
  assert.equal((err as CycleError).chain.length, 4); // X -> Y -> Z -> X
  assert.equal((err as CycleError).chain[0], (err as CycleError).chain[3]);
});

test('变换中途抛错：文本、占位树、依赖、撤销状态全部回滚', () => {
  const k = new TemplateKernel('ok - ok');
  k.addPlaceholder('P', 0, 2);
  k.addMirror('M', 5, 7, 'P', (s) => {
    if (s === 'bad') throw new Error('boom');
    return s;
  }, 'strict');

  k.edit('P', 'good'); // 先成功一次，累积一条撤销记录
  assert.equal(k.text, 'good - good');
  assert.equal(k.undoDepth, 1);

  const before = k.text;
  assert.throws(() => k.edit('P', 'bad'), /Transform "strict".*boom/);

  assert.equal(k.text, before);            // 文本回滚
  assert.equal(k.placeholderText('P'), 'good');
  assert.equal(k.placeholderText('M'), 'good'); // 占位树回滚
  assert.equal(k.undoDepth, 1);            // 撤销状态未污染
  assert.ok(k.isValid('P') && k.isValid('M'));

  k.edit('P', 'fine'); // 回滚后内核仍可正常工作
  assert.equal(k.text, 'fine - fine');
});

test('注册同步阶段的重入：抛 ReentrancyError 且注册无副作用', () => {
  const k = new TemplateKernel('a - a');
  k.addPlaceholder('P', 0, 1);
  assert.throws(
    () => k.addMirror('M', 4, 5, 'P', () => {
      k.edit('P', 'reentrant'); // 变换中试图再编辑
      return 'unreachable';
    }),
    ReentrancyError,
  );
  assert.equal(k.text, 'a - a');        // 文本未被污染
  assert.equal(k.isValid('M'), false);  // 占位未注册成功
  assert.equal(k.undoDepth, 0);
});

test('重入隔离（编辑传播阶段）', () => {
  const k = new TemplateKernel('a - a');
  k.addPlaceholder('P', 0, 1);
  let armed = false;
  k.addMirror('M', 4, 5, 'P', (s) => {
    if (armed) k.edit('P', 'reentrant');
    return s.toUpperCase();
  });

  armed = true;
  const before = k.text;
  const depth = k.undoDepth;
  assert.throws(() => k.edit('P', 'b'), ReentrancyError);
  assert.equal(k.text, before);       // 全部回滚
  assert.equal(k.undoDepth, depth);
  assert.equal(k.placeholderText('P'), 'a');
});

test('嵌套占位被父编辑覆盖后失效，撤销恢复原身份', () => {
  //            0123456789012
  const k = new TemplateKernel('say [world]!');
  k.addPlaceholder('OUTER', 4, 11);
  k.addPlaceholder('INNER', 5, 10, 'OUTER');
  assert.equal(k.placeholderText('INNER'), 'world');

  k.edit('OUTER', 'REPLACED');
  assert.equal(k.text, 'say REPLACED!');
  assert.equal(k.isValid('INNER'), false); // 子占位被覆盖失效
  assert.throws(() => k.edit('INNER', 'x'), InvalidatedPlaceholderError);
  assert.throws(() => k.placeholderText('INNER'), InvalidatedPlaceholderError);

  assert.equal(k.undo(), true);
  assert.equal(k.text, 'say [world]!');
  assert.equal(k.isValid('INNER'), true);           // 身份恢复
  assert.equal(k.placeholderText('INNER'), 'world'); // 内容恢复
  assert.deepEqual(k.placeholderRange('INNER'), { start: 5, end: 10 }); // 区间恢复
});

test('镜像源被父编辑覆盖：镜像级联失效，撤销恢复', () => {
  const k = new TemplateKernel('[src] - src');
  k.addPlaceholder('OUTER', 0, 5);
  k.addPlaceholder('SRC', 1, 4, 'OUTER');
  k.addMirror('M', 8, 11, 'SRC');
  assert.equal(k.text, '[src] - src');

  k.edit('OUTER', 'X');
  assert.equal(k.isValid('SRC'), false);
  assert.equal(k.isValid('M'), false); // 源失效 -> 镜像级联失效

  k.undo();
  assert.equal(k.isValid('SRC'), true);
  assert.equal(k.isValid('M'), true);
  assert.equal(k.placeholderText('M'), 'src');
});

test('UTF-16 偏移：禁止切开代理对', () => {
  // 'a😀b' => UTF-16: [a][D83D DE00][b]，emoji 占 [1,3)
  const k = new TemplateKernel('a\uD83D\uDE00b');
  assert.throws(() => k.addPlaceholder('BAD1', 2, 3), InvalidRangeError); // 起点切开代理对
  assert.throws(() => k.addPlaceholder('BAD2', 0, 2), InvalidRangeError); // 终点切开代理对

  k.addPlaceholder('EMOJI', 1, 3);
  k.addPlaceholder('TAIL', 3, 4);
  k.edit('EMOJI', 'xyz'); // 2 码元 -> 3 码元
  assert.equal(k.text, 'axyzb');
  assert.deepEqual(k.placeholderRange('TAIL'), { start: 4, end: 5 });
  assert.equal(k.placeholderText('TAIL'), 'b');
});

test('LF 与 CRLF 两种换行：镜像同步与偏移一致正确', () => {
  for (const nl of ['\n', '\r\n']) {
    const text = `line1${nl}body${nl}body`;
    const k = new TemplateKernel(text);
    const line2 = 6 + (nl.length - 1); // 'line1' + nl
    k.addPlaceholder('P', line2, line2 + 4);
    k.addMirror('M', line2 + 4 + nl.length, line2 + 8 + nl.length, 'P', (s) => s.toUpperCase());
    assert.equal(k.placeholderText('M'), 'BODY');

    k.edit('P', 'xy');
    assert.equal(k.text, `line1${nl}xy${nl}XY`);
    assert.equal(k.placeholderText('M'), 'XY');
    assert.deepEqual(k.placeholderRange('M'), {
      start: line2 + 2 + nl.length,
      end: line2 + 4 + nl.length,
    });
  }
});

test('退出占位模式保留最终纯文本', () => {
  const k = new TemplateKernel('hi - hi');
  k.addPlaceholder('P', 0, 2);
  k.addMirror('M', 5, 7, 'P');
  k.edit('P', 'yo');

  const out = k.finalize();
  assert.equal(out, 'yo - yo'); // 最终纯文本
  assert.throws(() => k.edit('P', 'x'), FinalizedError);
  assert.throws(() => k.text, FinalizedError);
});

test('撤销链：多次编辑逐步回退', () => {
  const k = new TemplateKernel('v0 - v0');
  k.addPlaceholder('P', 0, 2);
  k.addMirror('M', 5, 7, 'P');
  k.edit('P', 'v1');
  k.edit('P', 'v2');
  assert.equal(k.text, 'v2 - v2');
  k.undo();
  assert.equal(k.text, 'v1 - v1');
  k.undo();
  assert.equal(k.text, 'v0 - v0');
  assert.equal(k.undo(), false); // 撤销栈空
});
