import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runExperiment } from '../src/experiment.ts';

const result = runExperiment();

test('1000节点: 单节点每秒出入包数较扁平Gossip降低 >= 95%', () => {
  assert.ok(
    result.reductionPct >= 95,
    `reduction ${result.reductionPct.toFixed(2)}% < 95% ` +
      `(flat=${result.flatPerNodePerSec.toFixed(2)}/s, sub=${result.subPerNodePerSec.toFixed(2)}/s)`,
  );
});

test('1000节点: Cell内节点崩溃时, 全部Cell成员局部感知延迟 < 300ms', () => {
  assert.equal(
    result.cellPeersNotified,
    result.cellPeerTotal,
    `only ${result.cellPeersNotified}/${result.cellPeerTotal} cell peers detected the crash`,
  );
  assert.ok(
    result.maxCellDetectionMs < 300,
    `max cell detection latency ${result.maxCellDetectionMs}ms >= 300ms`,
  );
});

test('跨Cell汇聚: 崩溃事件经Leader低频广播传播到其他Cell', () => {
  assert.notEqual(result.crossCellLearnedMs, null, 'crash event never left the cell');
  assert.ok(
    (result.crossCellLearnedMs as number) < 5000,
    `cross-cell propagation took ${result.crossCellLearnedMs}ms`,
  );
});
