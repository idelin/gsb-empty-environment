import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { matchesCondition } from '../src/evaluate';
import { FlagConfig } from '../src/types';

const baseFlag: FlagConfig = {
  id: 'feature',
  defaultValue: 'off',
  overrides: { 'u-vip': 'on' },
  groups: [
    {
      id: 'g-beta',
      priority: 10,
      conditions: [{ key: 'tier', op: 'eq', value: 'beta' }],
      value: 'beta-value',
    },
    {
      id: 'g-staff',
      priority: 20,
      conditions: [{ key: 'tier', op: 'eq', value: 'beta' }],
      value: 'staff-value',
    },
  ],
};

test('precedence: explicit user override beats group rules', () => {
  const store = new FlagStore([baseFlag]);
  const r = store.evaluate('feature', { id: 'u-vip', attributes: { tier: 'beta' } });
  assert.strictEqual(r.value, 'on');
  assert.strictEqual(r.reason, 'override');
});

test('precedence: higher priority group wins', () => {
  const store = new FlagStore([baseFlag]);
  const r = store.evaluate('feature', { id: 'u-1', attributes: { tier: 'beta' } });
  assert.strictEqual(r.value, 'staff-value');
  assert.strictEqual(r.matchedGroupId, 'g-staff');
});

test('precedence: equal priority is settled by ascending group id', () => {
  const store = new FlagStore([
    {
      id: 'feature',
      defaultValue: 'off',
      groups: [
        { id: 'g-b', priority: 5, conditions: [], value: 'B' },
        { id: 'g-a', priority: 5, conditions: [], value: 'A' },
        { id: 'g-c', priority: 5, conditions: [], value: 'C' },
      ],
    },
  ]);
  const r = store.evaluate('feature', { id: 'u-1' });
  assert.strictEqual(r.value, 'A');
  assert.strictEqual(r.matchedGroupId, 'g-a');
});

test('precedence: falls through to default when no group matches', () => {
  const store = new FlagStore([baseFlag]);
  const r = store.evaluate('feature', { id: 'u-2', attributes: { tier: 'free' } });
  assert.strictEqual(r.value, 'off');
  assert.strictEqual(r.reason, 'default');
});

test('attributes: missing attribute is not the empty string', () => {
  const cond = { key: 'tier', op: 'eq' as const, value: '' };
  assert.strictEqual(matchesCondition(cond, undefined), false);
  assert.strictEqual(matchesCondition(cond, {}), false);
  assert.strictEqual(matchesCondition(cond, { tier: '' }), true);
  // neq on a missing attribute is also false: absent is not "not equal".
  assert.strictEqual(matchesCondition({ key: 'tier', op: 'neq', value: 'gold' }, {}), false);
  assert.strictEqual(matchesCondition({ key: 'tier', op: 'neq', value: 'gold' }, { tier: '' }), true);
  assert.strictEqual(matchesCondition({ key: 'tier', op: 'in', value: ['', 'gold'] }, {}), false);
  assert.strictEqual(matchesCondition({ key: 'tier', op: 'in', value: ['', 'gold'] }, { tier: '' }), true);
});

test('attributes: group requiring empty-string attr skips users missing it', () => {
  const store = new FlagStore([
    {
      id: 'feature',
      defaultValue: 'off',
      groups: [
        {
          id: 'g-empty',
          priority: 1,
          conditions: [{ key: 'nickname', op: 'eq', value: '' }],
          value: 'on',
        },
      ],
    },
  ]);
  assert.strictEqual(store.evaluate('feature', { id: 'u-1' }).value, 'off');
  assert.strictEqual(store.evaluate('feature', { id: 'u-1', attributes: {} }).value, 'off');
  // Attributes changed, so the caller bumps attrVersion to miss the cache.
  assert.strictEqual(
    store.evaluate('feature', { id: 'u-1', attributes: { nickname: '' }, attrVersion: 1 }).value,
    'on',
  );
});
