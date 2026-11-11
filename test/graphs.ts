import { GraphBuilder } from '../src/builder.ts';
import type { ProcedureGraph } from '../src/types.ts';

/**
 * 嵌套清理图：
 *   外层 region 持有 outer，内层 region 持有 inner；
 *   内层操作抛出 -> 异常边按 LIFO 展开（inner 先关、outer 后关）-> 处理器；
 *   正常路径退出内层、再退出外层（exitRegion 同样按 LIFO 关）。
 */
export function nestedCleanupGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('acqOuter', { op: 'acquire', variable: 'outer' })
    .addNode('enterR1', { op: 'enterRegion', region: 'r1' })
    .addNode('deferOuter', { op: 'deferClose', variable: 'outer', region: 'r1' })
    .addNode('acqInner', { op: 'acquire', variable: 'inner' })
    .addNode('enterR2', { op: 'enterRegion', region: 'r2' })
    .addNode('deferInner', { op: 'deferClose', variable: 'inner', region: 'r2' })
    .addNode('mayThrow', { op: 'noop' })
    .addNode('exitR2', { op: 'exitRegion', region: 'r2' })
    .addNode('exitR1', { op: 'exitRegion', region: 'r1' })
    .addNode('normalEnd', { op: 'noop' })
    .addNode('catch1', { op: 'catch' })
    .addNode('afterCatch', { op: 'noop' });

  b.addEdge('entry', 'acqOuter')
    .addEdge('acqOuter', 'enterR1')
    .addEdge('enterR1', 'deferOuter')
    .addEdge('deferOuter', 'acqInner')
    .addEdge('acqInner', 'enterR2')
    .addEdge('enterR2', 'deferInner')
    .addEdge('deferInner', 'mayThrow')
    .addEdge('mayThrow', 'exitR2')
    .addEdge('mayThrow', 'catch1', 'exception')
    .addEdge('exitR2', 'exitR1')
    .addEdge('exitR1', 'normalEnd')
    .addEdge('catch1', 'afterCatch');
  return b.build();
}

/**
 * 部分初始化失败图：
 *   先获取 a，尝试获取 b 时失败走异常边；汇合点 successOnly 只在成功路径可达，
 *   而 joinUse 同时被成功路径（b 已获取）和失败路径（b 未初始化）到达，
 *   借用 b 必须报 useUninitialized。a 在两条路径上都由延迟清理保证关闭。
 */
export function partialInitFailureGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('enterR', { op: 'enterRegion', region: 'r' })
    .addNode('acqA', { op: 'acquire', variable: 'a' })
    .addNode('deferA', { op: 'deferClose', variable: 'a', region: 'r' })
    // 尝试获取 b：成功节点才建立所有权；失败走异常边，b 从未初始化。
    .addNode('tryAcqB', { op: 'noop' })
    .addNode('acqB', { op: 'acquire', variable: 'b' })
    .addNode('catchB', { op: 'catch' })
    .addNode('failDiag', { op: 'noop' })
    .addNode('joinUse', { op: 'borrow', variable: 'b', handle: 'h_b' })
    .addNode('okUseB', { op: 'borrow', variable: 'b', handle: 'h_ok' })
    .addNode('failEnd', { op: 'noop' });

  b.addEdge('entry', 'enterR')
    .addEdge('enterR', 'acqA')
    .addEdge('acqA', 'deferA')
    .addEdge('deferA', 'tryAcqB')
    .addEdge('tryAcqB', 'acqB')
    .addEdge('acqB', 'okUseB')
    .addEdge('tryAcqB', 'catchB', 'exception', 'r')
    .addEdge('catchB', 'failDiag')
    .addEdge('failDiag', 'joinUse')
    .addEdge('okUseB', 'joinUse')
    .addEdge('joinUse', 'failEnd');
  return b.build();
}

/**
 * 异常重抛图：
 *   内层抛出被 catch2 捕获后显式重抛（rethrow 节点），
 *   重抛沿异常边展开外层 region；处理器中若把已关闭通道 ch 传给回调（借用），
 *   必须报 useAfterClose。
 */
export function rethrowGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('acqCh', { op: 'acquire', variable: 'ch' })
    .addNode('enterOuter', { op: 'enterRegion', region: 'outer' })
    .addNode('deferCh', { op: 'deferClose', variable: 'ch', region: 'outer' })
    .addNode('enterInner', { op: 'enterRegion', region: 'inner' })
    .addNode('acqTmp', { op: 'acquire', variable: 'tmp' })
    .addNode('deferTmp', { op: 'deferClose', variable: 'tmp', region: 'inner' })
    .addNode('innerThrow', { op: 'throw' })
    .addNode('catch2', { op: 'catch' })
    .addNode('rethrow', { op: 'throw' })
    .addNode('outerCatch', { op: 'catch' })
    .addNode('badCallback', { op: 'borrow', variable: 'ch', handle: 'cb' })
    .addNode('afterHandler', { op: 'noop' })
    .addNode('normalExitInner', { op: 'exitRegion', region: 'inner' })
    .addNode('normalExitOuter', { op: 'exitRegion', region: 'outer' })
    .addNode('normalEnd', { op: 'noop' });

  b.addEdge('entry', 'acqCh')
    .addEdge('acqCh', 'enterOuter')
    .addEdge('enterOuter', 'deferCh')
    .addEdge('deferCh', 'enterInner')
    .addEdge('enterInner', 'acqTmp')
    .addEdge('acqTmp', 'deferTmp')
    .addEdge('deferTmp', 'innerThrow')
    .addEdge('innerThrow', 'catch2', 'exception', 'inner')
    .addEdge('catch2', 'rethrow')
    .addEdge('rethrow', 'outerCatch', 'exception', 'outer')
    .addEdge('outerCatch', 'badCallback')
    .addEdge('badCallback', 'afterHandler')
    .addEdge('innerThrow', 'normalExitInner')
    .addEdge('normalExitInner', 'normalExitOuter')
    .addEdge('normalExitOuter', 'normalEnd');
  return b.build();
}

/**
 * 循环不动点图：
 *   循环体内借用 ch（句柄 h 在迭代尾归还），回边形成循环；
 *   退出循环后关闭 ch。验证分析在有限次迭代内收敛且不产生误报。
 */
export function loopGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('acq', { op: 'acquire', variable: 'ch' })
    .addNode('head', { op: 'noop' })
    .addNode('borrow', { op: 'borrow', variable: 'ch', handle: 'h' })
    .addNode('use', { op: 'noop' })
    .addNode('restore', { op: 'restore', handle: 'h' })
    .addNode('back', { op: 'noop' })
    .addNode('close', { op: 'close', variable: 'ch' })
    .addNode('end', { op: 'noop' });

  b.addEdge('entry', 'acq')
    .addEdge('acq', 'head')
    .addEdge('head', 'borrow')
    .addEdge('borrow', 'use')
    .addEdge('use', 'restore')
    .addEdge('restore', 'back')
    .addEdge('back', 'head')
    .addEdge('back', 'close')
    .addEdge('close', 'end');
  return b.build();
}

/** 循环体内借用不归还就关闭：必须报 borrowAcrossClose，且见证路径含回边。 */
export function loopBorrowAcrossCloseGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('acq', { op: 'acquire', variable: 'ch' })
    .addNode('head', { op: 'noop' })
    .addNode('borrow', { op: 'borrow', variable: 'ch', handle: 'h' })
    .addNode('back', { op: 'noop' })
    .addNode('close', { op: 'close', variable: 'ch' })
    .addNode('end', { op: 'noop' });

  b.addEdge('entry', 'acq')
    .addEdge('acq', 'head')
    .addEdge('head', 'borrow')
    .addEdge('borrow', 'back')
    .addEdge('back', 'head')
    .addEdge('back', 'close')
    .addEdge('close', 'end');
  return b.build();
}

/** 移动后原变量失效：move 之后再借用原变量报 useAfterMove。 */
export function moveGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('acq', { op: 'acquire', variable: 'x' })
    .addNode('move', { op: 'move', from: 'x', to: 'y' })
    .addNode('badBorrow', { op: 'borrow', variable: 'x', handle: 'h' })
    .addNode('goodBorrow', { op: 'borrow', variable: 'y', handle: 'h2' })
    .addNode('end', { op: 'noop' });
  b.addEdge('entry', 'acq')
    .addEdge('acq', 'move')
    .addEdge('move', 'badBorrow')
    .addEdge('badBorrow', 'goodBorrow')
    .addEdge('goodBorrow', 'end');
  return b.build();
}

/** 小图：覆盖获取/移动/借用/归还/关闭/异常展开，用于全序列枚举交叉验证。 */
export function smallGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('enterR', { op: 'enterRegion', region: 'r' })
    .addNode('acq', { op: 'acquire', variable: 'c' })
    .addNode('deferC', { op: 'deferClose', variable: 'c', region: 'r' })
    .addNode('borrow', { op: 'borrow', variable: 'c', handle: 'h' })
    .addNode('risky', { op: 'noop' })
    .addNode('restore', { op: 'restore', handle: 'h' })
    .addNode('move', { op: 'move', from: 'c', to: 'd' })
    .addNode('catch', { op: 'catch' })
    .addNode('exitR', { op: 'exitRegion', region: 'r' })
    .addNode('end', { op: 'noop' });

  b.addEdge('entry', 'enterR')
    .addEdge('enterR', 'acq')
    .addEdge('acq', 'deferC')
    .addEdge('deferC', 'borrow')
    .addEdge('borrow', 'risky')
    .addEdge('risky', 'restore')
    .addEdge('risky', 'catch', 'exception')
    .addEdge('restore', 'move')
    .addEdge('move', 'exitR')
    .addEdge('catch', 'exitR')
    .addEdge('exitR', 'end');
  return b.build();
}

/** 含确定违规的小图：异常路径延迟关闭后又立即关闭，必报 doubleClose。 */
export function smallBadGraph(): ProcedureGraph {
  const b = new GraphBuilder();
  b.addNode('entry', { op: 'entry' })
    .addNode('enterR', { op: 'enterRegion', region: 'r' })
    .addNode('acq', { op: 'acquire', variable: 'c' })
    .addNode('deferC', { op: 'deferClose', variable: 'c', region: 'r' })
    .addNode('risky', { op: 'noop' })
    .addNode('catch', { op: 'catch' })
    .addNode('badClose', { op: 'close', variable: 'c' })
    .addNode('exitR', { op: 'exitRegion', region: 'r' })
    .addNode('end', { op: 'noop' });

  b.addEdge('entry', 'enterR')
    .addEdge('enterR', 'acq')
    .addEdge('acq', 'deferC')
    .addEdge('deferC', 'risky')
    .addEdge('risky', 'exitR')
    .addEdge('risky', 'catch', 'exception')
    .addEdge('catch', 'badClose')
    .addEdge('badClose', 'exitR')
    .addEdge('exitR', 'end');
  return b.build();
}
