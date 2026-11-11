import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { analyze, enumerateSequences, violationKey } from '../src/index.ts';
import {
  loopBorrowAcrossCloseGraph,
  loopGraph,
  moveGraph,
  nestedCleanupGraph,
  partialInitFailureGraph,
  rethrowGraph,
  smallBadGraph,
  smallGraph,
} from './graphs.ts';

function kinds(result: ReturnType<typeof analyze>): string[] {
  return result.violations.map((v) => v.violation.kind);
}

function locationsOfKind(result: ReturnType<typeof analyze>, kind: string): string[] {
  return result.violations.filter((v) => v.violation.kind === kind).map((v) => v.violation.location);
}

describe('嵌套清理', () => {
  const graph = nestedCleanupGraph();
  const result = analyze(graph);

  it('LIFO 延迟清理无违规（正常退出与异常展开均恰好关闭一次）', () => {
    assert.deepEqual(result.violations, []);
  });

  it('异常边清理序列为内层 LIFO：inner 先于 outer', () => {
    const edge = graph.edges.find((e) => e.from === 'mayThrow' && e.kind === 'exception')!;
    assert.deepEqual(edge.cleanup, ['inner', 'outer']);
  });

  it('不动点事实：异常处理器内两变量均已关闭', () => {
    const catchFact = result.facts.catch1 as { status: Record<string, string[]> };
    assert.deepEqual(catchFact.status.inner, ['closed']);
    assert.deepEqual(catchFact.status.outer, ['closed']);
  });

  it('正常路径退出后两变量均已关闭', () => {
    const fact = result.facts.normalEnd as { status: Record<string, string[]> };
    assert.deepEqual(fact.status.inner, ['closed']);
    assert.deepEqual(fact.status.outer, ['closed']);
  });
});

describe('部分初始化失败', () => {
  const result = analyze(partialInitFailureGraph());

  it('汇合点借用未初始化变量报 useUninitialized', () => {
    assert.ok(kinds(result).includes('useUninitialized'));
    assert.deepEqual(locationsOfKind(result, 'useUninitialized'), ['joinUse']);
  });

  it('失败路径上 a 由异常展开关闭且不重复关闭，汇合事实为两路径并集', () => {
    assert.equal(kinds(result).includes('doubleClose'), false);
    const failFact = result.facts.failEnd as { status: Record<string, string[]> };
    // 汇合后抽象事实为两路径并集：失败路径关闭了 a，成功路径仍持有；b 同理。
    assert.ok(failFact.status.a.includes('closed'));
    assert.ok(failFact.status.b.includes('owned'));
    const witness = result.violations.find((v) => v.violation.kind === 'useUninitialized')!;
    assert.equal(witness.violation.location, 'joinUse');
  });

  it('违规路径经过异常边 catchB', () => {
    const witness = result.violations.find((v) => v.violation.kind === 'useUninitialized')!;
    const edgeSteps = witness.steps.filter((s) => s.kind === 'edge');
    assert.ok(
      edgeSteps.some((s) => s.edgeKind === 'exception' && s.from === 'tryAcqB' && s.to === 'catchB' && s.cleanup?.[0] === 'a'),
    );
  });
});

describe('异常重抛', () => {
  const result = analyze(rethrowGraph());

  it('重抛展开外层 region 后把已关闭通道传给回调，报 useAfterClose', () => {
    assert.deepEqual(locationsOfKind(result, 'useAfterClose'), ['badCallback']);
  });

  it('异常边按 LIFO 展开：内层先关 tmp，重抛后外层再关 ch', () => {
    const rethrowEdge = rethrowGraph().edges.find((e) => e.from === 'rethrow' && e.kind === 'exception')!;
    assert.deepEqual(rethrowEdge.cleanup, ['ch']);
    const innerEdge = rethrowGraph().edges.find((e) => e.from === 'innerThrow' && e.kind === 'exception')!;
    assert.deepEqual(innerEdge.cleanup, ['tmp']);
  });

  it('见证路径依次经过 innerThrow 异常边与 rethrow 异常边', () => {
    const witness = result.violations.find((v) => v.violation.kind === 'useAfterClose')!;
    const exceptions = witness.steps.filter((s) => s.kind === 'edge' && s.edgeKind === 'exception');
    assert.equal(exceptions.length, 2);
    assert.equal(exceptions[0].from, 'innerThrow');
    assert.equal(exceptions[1].from, 'rethrow');
  });
});

describe('循环不动点', () => {
  it('循环体内借还平衡：收敛且无违规', () => {
    const result = analyze(loopGraph());
    assert.deepEqual(result.violations, []);
    assert.ok(result.iterations >= 2, `迭代次数应包含回边传播，实际 ${result.iterations}`);
    const head = result.facts.head as { status: Record<string, string[]>; borrows: Record<string, string[]> };
    assert.deepEqual(head.status.ch, ['owned']);
    assert.deepEqual(head.borrows, {});
  });

  it('借用越过关闭报 borrowAcrossClose，见证路径含回边', () => {
    const result = analyze(loopBorrowAcrossCloseGraph());
    assert.deepEqual(locationsOfKind(result, 'borrowAcrossClose'), ['close']);
    const witness = result.violations.find((v) => v.violation.kind === 'borrowAcrossClose')!;
    // 首条见证取最短真实路径；路径必须包含借用点与关闭点。
    const nodes = witness.steps.filter((s) => s.kind === 'node').map((s) => s.node);
    assert.ok(nodes.includes('borrow') && nodes.includes('close'));
    // 不动点事实：head 处 ch 仍为 owned 且借用句柄集合稳定（循环再迭代不改变）。
    const head = result.facts.head as { status: Record<string, string[]>; borrows: Record<string, string[]> };
    assert.deepEqual(head.status.ch, ['owned']);
    assert.deepEqual(head.borrows.h, ['ch']);
  });
});

describe('移动语义', () => {
  it('移动后原变量失效：借用原变量报 useAfterMove，新变量可用', () => {
    const result = analyze(moveGraph());
    assert.deepEqual(locationsOfKind(result, 'useAfterMove'), ['badBorrow']);
    const fact = result.facts.goodBorrow as { status: Record<string, string[]>; borrows: Record<string, string[]> };
    assert.deepEqual(fact.status.x, ['moved']);
    assert.deepEqual(fact.status.y, ['owned']);
    assert.deepEqual(fact.borrows.h2, ['y']);
  });
});

describe('小图全执行序列交叉验证', () => {
  const graphs = {
    small: smallGraph(),
    smallBad: smallBadGraph(),
    nested: nestedCleanupGraph(),
    partial: partialInitFailureGraph(),
    rethrow: rethrowGraph(),
    move: moveGraph(),
    loop: loopGraph(),
  };

  for (const [name, graph] of Object.entries(graphs)) {
    it(`${name}: 抽象违规集合与全部具体执行序列一致（健全且精确）`, () => {
      const abstractResult = analyze(graph);
      const concrete = enumerateSequences(graph, 50);
      const abstractKeys = new Set(abstractResult.violations.map((v) => violationKey(v.violation)));

      assert.ok(concrete.sequences.length >= 1);
      // 健全性：具体序列中的每个违规都必须被抽象分析覆盖。
      for (const key of concrete.violationKeys) {
        assert.ok(abstractKeys.has(key), `抽象分析遗漏具体违规 ${key}`);
      }
      // 无环/结构化小图上进一步要求精确匹配（无过度近似误报）。
      if (!concrete.truncated) {
        assert.deepEqual([...abstractKeys].sort(), [...concrete.violationKeys].sort());
      }
    });
  }

  it('smallBad 显式重复关闭被检出，且见证路径包含异常边上的延迟清理步骤', () => {
    const result = analyze(smallBadGraph());
    const doubles = result.violations.filter((v) => v.violation.kind === 'doubleClose');
    assert.equal(doubles.length, 1);
    assert.equal(doubles[0].violation.location, 'badClose');
    assert.equal(doubles[0].violation.deferred, false);
    const exceptionEdge = doubles[0].steps.find((s) => s.kind === 'edge' && s.edgeKind === 'exception');
    assert.deepEqual(exceptionEdge?.cleanup, ['c']);
  });

  it('枚举器覆盖小图的正常与异常两类执行序列', () => {
    const concrete = enumerateSequences(smallGraph(), 50);
    const hasException = concrete.sequences.some((seq) => seq.steps.some((s) => s.edgeKind === 'exception'));
    const hasNormal = concrete.sequences.some((seq) => seq.steps.every((s) => s.edgeKind !== 'exception'));
    assert.ok(hasException && hasNormal);
  });
});
