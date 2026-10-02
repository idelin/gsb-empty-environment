import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { MembershipTable } from '../src/membership.ts';

describe('MembershipTable', () => {
  it('alive refutes suspect at the same incarnation', () => {
    const t = new MembershipTable();
    t.apply({ type: 'join', nodeId: 'a', incarnation: 0 });
    t.apply({ type: 'suspect', nodeId: 'a', incarnation: 0 });
    assert.equal(t.statusOf('a'), 'suspect');
    t.apply({ type: 'alive', nodeId: 'a', incarnation: 0 });
    assert.equal(t.statusOf('a'), 'alive');
  });

  it('ignores stale events with older incarnation', () => {
    const t = new MembershipTable();
    t.apply({ type: 'alive', nodeId: 'a', incarnation: 5 });
    assert.equal(t.apply({ type: 'suspect', nodeId: 'a', incarnation: 4 }), false);
    assert.equal(t.statusOf('a'), 'alive');
  });

  it('leave marks the member dead terminally', () => {
    const t = new MembershipTable();
    t.apply({ type: 'join', nodeId: 'a', incarnation: 0 });
    t.apply({ type: 'leave', nodeId: 'a', incarnation: 0 });
    assert.equal(t.statusOf('a'), 'dead');
  });
});
