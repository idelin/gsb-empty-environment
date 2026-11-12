import assert from 'node:assert/strict';
import test from 'node:test';

import {
  ConflictResolver,
  chooseStreamState,
  resolveConflict,
  resolveConflictAsync,
  type AnchorProof,
  type Commit,
  type StreamState,
} from '../index.ts';

function commit(cid: string, anchorTimestamp?: number, forged = false): Commit {
  if (anchorTimestamp === undefined) return { cid };
  return {
    cid,
    anchor: {
      blockTimestamp: anchorTimestamp,
      forged,
    },
  };
}

function state(...commits: Commit[]): StreamState {
  return { log: commits };
}

const verifyNotForged = (proof: AnchorProof): boolean => proof.forged !== true;

test('anchored branch beats a longer unanchored branch', () => {
  const current = state(
    commit('root'),
    commit('current-update'),
    commit('current-anchor', 100),
  );
  const incoming = state(
    commit('root'),
    commit('attack-1'),
    commit('attack-2'),
    commit('attack-3'),
    commit('attack-4'),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'current');
  assert.equal(result.reason, 'current-anchored-over-unanchored');
  assert.equal(result.tip, 'current-anchor');
  assert.equal(result.current.anchored, true);
  assert.equal(result.incoming.anchored, false);
  assert.ok(result.incoming.length > result.current.length);
  assert.equal(result.selected, current);
});

test('earlier anchor block timestamp wins when both branches are anchored', () => {
  const current = state(
    commit('root'),
    commit('current-anchor', 20),
    commit('current-update'),
  );
  const incoming = state(
    commit('root'),
    commit('incoming-anchor', 10),
    commit('incoming-update-1'),
    commit('incoming-update-2'),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'earlier-incoming-anchor');
  assert.equal(result.incoming.anchor?.timestamp, 10);
  assert.equal(result.current.anchor?.timestamp, 20);
});

test('equal anchor timestamps select the longer branch', () => {
  const current = state(
    commit('root-anchored', 50),
    commit('current-update'),
  );
  const incoming = state(
    commit('root-anchored', 50),
    commit('incoming-update-1'),
    commit('incoming-update-2'),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'longer-incoming');
  assert.equal(result.commonPrefixLength, 1);
  assert.equal(result.forkIndex, 0);
  assert.equal(result.current.anchored, false);
  assert.equal(result.incoming.anchored, false);
  assert.equal(result.current.anchors[0].timestamp, 50);
  assert.equal(result.incoming.anchors[0].timestamp, 50);
});

test('equal length and anchor time fall back to smaller tip CID', () => {
  const current = state(
    commit('root-anchored', 50),
    commit('bafy-z-current-tip'),
  );
  const incoming = state(
    commit('root-anchored', 50),
    commit('bafy-a-incoming-tip'),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'smaller-incoming-tip-cid');
  assert.equal(result.tip, 'bafy-a-incoming-tip');
});

test('longer unanchored branch wins when neither branch is anchored', () => {
  const current = state(commit('root'), commit('current-1'));
  const incoming = state(commit('root'), commit('incoming-1'), commit('incoming-2'));

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'longer-incoming');
});

test('a fresh post-fork anchor beats an extension that only shares an old anchor', () => {
  const current = state(
    commit('root-anchored', 50),
    commit('current-update'),
  );
  const incoming = state(
    commit('root-anchored', 50),
    commit('incoming-anchor', 200),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'incoming-anchored-over-unanchored');
  assert.equal(result.current.anchored, false);
  assert.equal(result.incoming.anchored, true);
});

test('equal unanchored branches use the smaller tip CID', () => {
  const current = state(commit('root'), commit('bafy-z'));
  const incoming = state(commit('root'), commit('bafy-a'));

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'smaller-incoming-tip-cid');
});

test('a branch forked before the current anchor must anchor strictly earlier', () => {
  const current = state(
    commit('root'),
    commit('current-update'),
    commit('current-anchor', 100),
  );

  const sameTimestamp = state(
    commit('root'),
    commit('incoming-update'),
    commit('incoming-anchor', 100),
    commit('incoming-tail'),
  );
  const laterTimestamp = state(
    commit('root'),
    commit('incoming-update'),
    commit('incoming-anchor', 200),
    commit('incoming-tail'),
  );

  for (const incoming of [sameTimestamp, laterTimestamp]) {
    const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });
    assert.equal(result.winner, 'current');
    assert.equal(result.reason, 'fork-anchor-guard');
    assert.equal(result.current.anchor?.index, 2);
    assert.equal(result.forkIndex, 0);
  }
});

test('a strictly earlier independent anchor can replace an anchored history', () => {
  const current = state(
    commit('root'),
    commit('current-update'),
    commit('current-anchor', 100),
  );
  const incoming = state(
    commit('root'),
    commit('incoming-update'),
    commit('incoming-anchor', 50),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'earlier-incoming-anchor');
});

test('a forged anchor is treated as unanchored', () => {
  const current = state(
    commit('root'),
    commit('current-update'),
    commit('current-anchor', 100),
  );
  const forgedLonger = state(
    commit('root'),
    commit('attack-1'),
    commit('attack-2'),
    commit('attack-3', 1, true),
    commit('attack-4'),
  );

  const result = resolveConflict(current, forgedLonger, {
    verifyAnchor: verifyNotForged,
  });

  assert.equal(result.winner, 'current');
  assert.equal(result.reason, 'current-anchored-over-unanchored');
  assert.equal(result.incoming.anchored, false);
  assert.equal(result.incoming.anchor, null);
});

test('a forged current anchor does not protect it from a valid incoming anchor', () => {
  const current = state(
    commit('root'),
    commit('current-anchor', 10, true),
  );
  const incoming = state(
    commit('root'),
    commit('incoming-anchor', 100),
  );

  const result = resolveConflict(current, incoming, {
    verifyAnchor: verifyNotForged,
  });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'incoming-anchored-over-unanchored');
  assert.equal(result.current.anchored, false);
});

test('a verifier rejection wins over a longer forged history', () => {
  const current = state(commit('root'), commit('current-anchor', 10));
  const forged = state(
    commit('root'),
    commit('one'),
    commit('two'),
    commit('three', 5, true),
  );

  const result = resolveConflict(current, forged, {
    verifyAnchor: verifyNotForged,
  });

  assert.equal(result.winner, 'current');
  assert.equal(result.current.anchored, true);
  assert.equal(result.incoming.anchored, false);
});

test('verifier receives side, commit and index context', () => {
  const current = state(commit('root'), commit('current-anchor', 10));
  const incoming = state(commit('root'), commit('incoming-anchor', 20));
  const seen: string[] = [];

  resolveConflict(current, incoming, {
    verifyAnchor: (_proof, _commit, context) => {
      seen.push(`${context.side}:${context.index}`);
      return true;
    },
  });

  assert.deepEqual(seen.sort(), ['current:1', 'incoming:1']);
});

test('a verifier can be passed directly as the third argument', () => {
  const current = state(commit('root'), commit('current-anchor', 10));
  const incoming = state(
    commit('root'),
    commit('incoming-1'),
    commit('incoming-2'),
  );

  const result = resolveConflict(current, incoming, () => true);

  assert.equal(result.winner, 'current');
  assert.equal(result.current.anchored, true);
});

test('async verifier failures are treated as unanchored', async () => {
  const current = state(commit('root'), commit('current-anchor', 10));
  const forgedLonger = state(
    commit('root'),
    commit('a'),
    commit('b'),
    commit('c', 5, true),
    commit('d'),
  );

  const result = await resolveConflictAsync(current, forgedLonger, {
    verifyAnchorAsync: async (proof) => proof.forged !== true,
  });

  assert.equal(result.winner, 'current');
  assert.equal(result.reason, 'current-anchored-over-unanchored');
});

test('an unanchored descendant extension is accepted as the new tip', () => {
  const current = state(
    commit('root'),
    commit('current-anchor', 10),
  );
  const incoming = state(
    commit('root'),
    commit('current-anchor', 10),
    commit('next-update'),
  );

  const result = resolveConflict(current, incoming, { verifyAnchor: verifyNotForged });

  assert.equal(result.winner, 'incoming');
  assert.equal(result.reason, 'incoming-descends-from-current');
  assert.equal(result.tip, 'next-update');
});

test('compact summaries can be compared with an explicit fork index', () => {
  const current: StreamState = {
    tip: 'current-tip',
    length: 3,
    anchored: true,
    anchorTimestamp: 10,
    anchorIndex: 1,
  };
  const incoming: StreamState = {
    tip: 'incoming-tip',
    length: 7,
    anchored: false,
  };

  const result = resolveConflict(current, incoming, {
    commonPrefixLength: 1,
    verifyAnchor: verifyNotForged,
  });

  assert.equal(result.winner, 'current');
  assert.equal(result.reason, 'current-anchored-over-unanchored');
});

test('ConflictResolver and chooseStreamState expose the same decision', () => {
  const current = state(commit('root'), commit('current-anchor', 10));
  const incoming = state(
    commit('root'),
    commit('incoming-1'),
    commit('incoming-2'),
  );
  const resolver = new ConflictResolver({ verifyAnchor: verifyNotForged });

  assert.equal(resolver.resolve(current, incoming).winner, 'current');
  assert.equal(chooseStreamState(current, incoming, { verifyAnchor: verifyNotForged }), current);
});
