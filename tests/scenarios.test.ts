import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bindForms } from '../index.ts';
import {
  crossFormFixture,
  duplicateLabelFixture,
  explicitPriorityFixture,
  hiddenControlFixture,
  nestedDialogFixture,
} from './fixtures.ts';

test('cross-form ancestors: labels bind only inside their own form', () => {
  const result = bindForms(crossFormFixture().nodes, [], { ambiguityThreshold: 0 });
  assert.equal(result.accepted, true);
  const byLabel = new Map(result.fields.map((field) => [field.labelId, field]));
  assert.equal(byLabel.get('a-label-1')!.controlId, 'a-input-1');
  assert.equal(byLabel.get('a-label-2')!.controlId, 'a-input-2');
  assert.equal(byLabel.get('b-label-1')!.controlId, 'b-input-1');
  assert.equal(byLabel.get('b-label-2')!.controlId, 'b-input-2');
  for (const field of result.fields) {
    assert.equal(field.edge?.forbidden, false);
    assert.ok(field.candidates.some((edge) => edge.forbidden));
  }
});

test('duplicate labels: near-tie margins are rejected when threshold is high', () => {
  const loose = bindForms(duplicateLabelFixture().nodes, [], { ambiguityThreshold: 0 });
  assert.equal(loose.accepted, true);
  const strict = bindForms(duplicateLabelFixture().nodes, [], { ambiguityThreshold: 10 });
  assert.equal(strict.accepted, false);
  assert.ok(strict.globalMargin !== null && strict.globalMargin < 10);
  assert.ok(strict.diagnostics.join(' ').includes('margin'));
});

test('hidden control: excluded by default, eligible with includeHidden', () => {
  const defaultResult = bindForms(hiddenControlFixture().nodes);
  const hiddenField = defaultResult.fields.find((field) => field.labelId === 'hid-label')!;
  assert.equal(hiddenField.status, 'unbound');
  const hiddenEdge = hiddenField.candidates.find((edge) => edge.controlId === 'hid-input')!;
  assert.equal(hiddenEdge.forbidden, true);
  assert.ok(hiddenField.reasons.join(' ').includes('hidden'));

  const included = bindForms(hiddenControlFixture().nodes, [], { includeHidden: true });
  const hiddenNow = included.fields.find((field) => field.labelId === 'hid-label')!;
  assert.equal(hiddenNow.controlId, 'hid-input');
  assert.ok(hiddenNow.edge?.reasons.join(' ').includes('includeHidden=true'));
});

test('nested dialog: modal labels never bind outer-form controls', () => {
  const result = bindForms(nestedDialogFixture().nodes, [], { ambiguityThreshold: 0 });
  assert.equal(result.accepted, true);
  const outer = result.fields.find((field) => field.labelId === 'outer-label')!;
  const modal = result.fields.find((field) => field.labelId === 'modal-label')!;
  assert.equal(outer.controlId, 'outer-input');
  assert.equal(modal.controlId, 'modal-input');
  const forbiddenModalToOuter = modal.candidates.find((edge) => edge.controlId === 'outer-input')!;
  assert.equal(forbiddenModalToOuter.forbidden, true);
});

test('explicit relation takes priority over geometric proximity', () => {
  const result = bindForms(
    explicitPriorityFixture().nodes,
    explicitPriorityFixture().explicit,
  );
  assert.equal(result.accepted, true);
  const field = result.fields.find((item) => item.labelId === 'exp-label')!;
  assert.equal(field.source, 'explicit');
  assert.equal(field.controlId, 'far-input');
  assert.equal(field.explicitKind, 'for');
  assert.equal(field.candidates.length, 1);
});

test('role mismatch penalty changes the chosen control', () => {
  const nodes = [
    { id: 'dlg', role: 'dialog', parentId: null, rect: { x: 0, y: 0, width: 400, height: 200 } },
    { id: 'lbl', role: 'label', isLabel: true, name: 'Agree', parentId: 'dlg', rect: { x: 10, y: 20, width: 60, height: 24 }, expectedControlRoles: ['checkbox'] },
    // Closer but wrong role (gap 20 -> penalized edge ~70).
    { id: 'close-text', role: 'textbox', parentId: 'dlg', rect: { x: 90, y: 20, width: 80, height: 24 } },
    // Farther but the expected role (gap 60 -> clean edge ~60).
    { id: 'far-check', role: 'checkbox', parentId: 'dlg', rect: { x: 130, y: 20, width: 20, height: 20 } },
  ];
  const result = bindForms(nodes, [], { ambiguityThreshold: 0 });
  const field = result.fields[0];
  assert.equal(field.controlId, 'far-check');
  const textboxEdge = field.candidates.find((edge) => edge.controlId === 'close-text')!;
  assert.equal(textboxEdge.roleMismatch, true);
});

test('weights are caller-supplied: zeroing the role penalty flips the match', () => {
  const nodes = [
    { id: 'dlg', role: 'dialog', parentId: null, rect: { x: 0, y: 0, width: 400, height: 200 } },
    { id: 'lbl', role: 'label', isLabel: true, name: 'Agree', parentId: 'dlg', rect: { x: 10, y: 20, width: 60, height: 24 }, expectedControlRoles: ['checkbox'] },
    { id: 'close-text', role: 'textbox', parentId: 'dlg', rect: { x: 90, y: 20, width: 80, height: 24 } },
    { id: 'far-check', role: 'checkbox', parentId: 'dlg', rect: { x: 130, y: 20, width: 20, height: 20 } },
  ];
  const result = bindForms(nodes, [], {
    ambiguityThreshold: 0,
    weights: { distance: 1, wrongDirection: 100, roleMismatch: 0 },
  });
  assert.equal(result.fields[0].controlId, 'close-text');
});

test('evidence is emitted for every field with sorted candidate costs', () => {
  const result = bindForms(crossFormFixture().nodes, [], { ambiguityThreshold: 0 });
  for (const field of result.fields) {
    assert.ok(field.labelId);
    assert.ok(['bound', 'ambiguous', 'unbound'].includes(field.status));
    assert.ok(field.reasons.length > 0);
    const costs = field.candidates.map((edge) => edge.cost);
    assert.deepEqual(costs, [...costs].sort((a, b) => a - b));
    for (const edge of field.candidates) {
      assert.ok(typeof edge.rawDistance === 'number');
      assert.ok(typeof edge.direction === 'string');
      assert.ok(Array.isArray(edge.reasons));
    }
  }
  assert.ok(JSON.stringify(result).length > 0);
});
