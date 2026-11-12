import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bindForms, bestAndSecondBest, FORBIDDEN_COST } from '../index.ts';
import { evaluateEdge } from '../src/cost.ts';
import { indexTree } from '../src/tree.ts';
import { DEFAULT_FORM_ROLES, DEFAULT_PREFERRED_DIRECTIONS, DEFAULT_WEIGHTS, type EdgeCost } from '../src/types.ts';
import { allScenarios, crossFormFixture, type Scenario } from './fixtures.ts';

/**
 * Reconstruct the exact cost matrix the binder feeds to the Hungarian
 * solver (explicitly consumed edges removed), then enumerate every
 * possible assignment on these small trees and assert the Hungarian
 * optimum matches the brute-force optimum.
 */
function implicitCostMatrix(scenario: Scenario): {
  costs: number[][];
  labelIds: string[];
  controlIds: string[];
} {
  const index = indexTree(scenario.nodes, DEFAULT_FORM_ROLES);
  const occupied = new Set(
    scenario.explicit
      .filter((rel) => {
        const control = index.nodes.get(rel.controlId);
        return Boolean(control && !control.hidden && control.rect);
      })
      .map((rel) => rel.controlId),
  );
  const explicitLabelIds = new Set(scenario.explicit.map((rel) => rel.labelId));
  const labelIds = scenario.nodes
    .filter((node) => node.isLabel && !explicitLabelIds.has(node.id))
    .map((node) => node.id);
  const controlIds = scenario.nodes
    .filter(
      (node) =>
        !node.isLabel &&
        !occupied.has(node.id) &&
        !DEFAULT_FORM_ROLES.includes(node.role),
    )
    .map((node) => node.id);

  const costs = labelIds.map((labelId) =>
    controlIds.map((controlId) => {
      const edge: EdgeCost = evaluateEdge(
        index.nodes.get(labelId)!,
        index.nodes.get(controlId)!,
        {
          index,
          weights: DEFAULT_WEIGHTS,
          preferred: new Set(DEFAULT_PREFERRED_DIRECTIONS),
          includeHidden: false,
          occupiedControls: occupied,
        },
      );
      return edge.cost;
    }),
  );
  return { costs, labelIds, controlIds };
}

test('Hungarian optimum equals full enumeration on every small tree', () => {
  for (const scenario of allScenarios()) {
    const { costs } = implicitCostMatrix(scenario);
    if (costs.length === 0 || costs[0].length === 0) {
      continue;
    }
    const { best, second } = bestAndSecondBest(costs);
    const result = bindForms(scenario.nodes, scenario.explicit, { ambiguityThreshold: 0 });
    assert.ok(best, `${scenario.name}: enumeration produced no assignments`);
    assert.ok(result.bestCost !== null, `${scenario.name}: binder returned no best cost`);
    assert.ok(
      Math.abs(result.bestCost - best.cost) < 1e-9,
      `${scenario.name}: hungarian ${result.bestCost} != enumeration ${best.cost}`,
    );
    if (second) {
      assert.ok(
        result.secondBestCost !== null &&
          Math.abs(result.secondBestCost - second.cost) < 1e-9,
        `${scenario.name}: second-best mismatch`,
      );
    }
  }
});

test('forbidden edges carry the sentinel and enumeration never selects them', () => {
  const scenario = crossFormFixture();
  const { costs, labelIds, controlIds } = implicitCostMatrix(scenario);
  for (let row = 0; row < labelIds.length; row += 1) {
    for (let col = 0; col < controlIds.length; col += 1) {
      const crossForm = labelIds[row].startsWith('a-') !== controlIds[col].startsWith('a-');
      if (crossForm) {
        assert.equal(costs[row][col], FORBIDDEN_COST);
      }
    }
  }
  const { best } = bestAndSecondBest(costs);
  assert.ok(best);
  for (const col of best.rowToCol) {
    assert.notEqual(col, -1);
  }
});
