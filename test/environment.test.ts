import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BrowserEnvironment } from '../src/environment.ts';

test('导航只递增文档代，不改变焦点代', () => {
  const env = new BrowserEnvironment();
  env.openTab('a', { focused: true });
  const before = env.snapshot('a');
  env.navigate('a');
  const after = env.snapshot('a');

  assert.equal(after.documentGeneration, before.documentGeneration + 1);
  assert.equal(after.focusGeneration, before.focusGeneration);
});

test('多标签页切换使原标签页焦点代快照失配', () => {
  const env = new BrowserEnvironment();
  env.openTab('a', { focused: true });
  env.openTab('b');
  const snapshotA = env.snapshot('a');

  env.focusTab('b');
  const currentA = env.snapshot('a');

  assert.equal(currentA.documentGeneration, snapshotA.documentGeneration);
  assert.notEqual(currentA.focusGeneration, snapshotA.focusGeneration);
  assert.equal(env.isFocused('a'), false);
  assert.equal(env.isFocused('b'), true);
});

test('关闭标签页后无法取得新快照', () => {
  const env = new BrowserEnvironment();
  env.openTab('a', { focused: true });
  env.closeTab('a');
  assert.equal(env.hasTab('a'), false);
  assert.equal(env.getCurrentSnapshot('a'), null);
  assert.equal(env.getFocusedTabId(), null);
});
