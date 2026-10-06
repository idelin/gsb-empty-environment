import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { HistoryStore } from '../src/history-store.ts';
import {
  BranchExistsError,
  DetachedHeadError,
  UnknownBranchError,
  UnknownVersionError,
} from '../src/errors.ts';

const add = (state: number, event: number): number => state + event;
const makeStore = () => new HistoryStore<number, number>(add, 0);

describe('HistoryStore: viewing old versions', () => {
  it('checkout views an old version without moving the real head', () => {
    const store = makeStore();
    store.commit(1);
    const v2 = store.head;
    store.commit(2);
    const headBefore = store.head;

    const view = store.checkout(v2.id);

    assert.equal(view.state, 1);
    assert.equal(view.isHead, false);
    assert.equal(store.head.id, headBefore.id, 'head untouched by checkout');
    assert.equal(store.head.state, 3);
    assert.equal(store.activeBranchName, 'main');
  });

  it('new events keep appending to the head while a view is open', () => {
    const store = makeStore();
    store.commit(1);
    const v2 = store.head;
    const view = store.checkout(v2.id);

    store.commit(2);
    store.commit(3);

    assert.equal(store.head.state, 6, 'commits landed on the real head');
    assert.equal(view.state, 1, 'view stays pinned to the old version');
    assert.equal(view.closed, false);
    assert.equal(store.versionTree.tipOf('main').id, store.head.id);
  });

  it('checkout of an unknown version throws', () => {
    const store = makeStore();
    assert.throws(() => store.checkout('v999'), UnknownVersionError);
  });
});

describe('HistoryStore: editing from an old version requires a branch', () => {
  it('view.commit is refused and cannot overwrite main', () => {
    const store = makeStore();
    store.commit(1);
    const v2 = store.head;
    store.commit(2);
    const mainTip = store.head;

    const view = store.checkout(v2.id);
    assert.throws(() => view.commit(100), DetachedHeadError);

    assert.equal(store.head.id, mainTip.id, 'main tip unchanged');
    assert.equal(store.versionTree.size, 3, 'no version was appended');
  });

  it('view.fork explicitly creates a branch; edits land there, not on main', () => {
    const store = makeStore();
    store.commit(1);
    const v2 = store.head;
    store.commit(2);
    const mainTip = store.head;
    const treeBefore = store.versionTree;

    const view = store.checkout(v2.id);
    view.fork('fix');
    store.commit(100);

    assert.equal(store.activeBranchName, 'fix');
    assert.equal(store.head.state, 101, 'commit applied on top of the fork point');
    assert.equal(store.versionTree.tipOf('main').id, mainTip.id, 'main not overwritten');
    assert.equal(store.versionTree.tipOf('fix').id, store.head.id);
    assert.equal(
      store.versionTree.node(store.head.id).parentId,
      v2.id,
      'branch grew from the historical version',
    );
    assert.equal(view.closed, true, 'fork closes the view');

    assert.equal(treeBefore.size, 3, 'persistent snapshot from before fork is intact');
    assert.equal(treeBefore.hasBranch('fix'), false);
    assert.equal(treeBefore.tipOf('main').id, mainTip.id);
  });

  it('branches cannot be overwritten: duplicate names are rejected', () => {
    const store = makeStore();
    store.commit(1);
    const v2 = store.head;
    store.createBranch('fix', v2.id);

    assert.throws(() => store.createBranch('fix', v2.id), BranchExistsError);
    assert.throws(() => store.createBranch('main', v2.id), BranchExistsError);
    assert.throws(() => store.createBranch('other', 'v999'), UnknownVersionError);
    assert.throws(() => store.switchBranch('nope'), UnknownBranchError);
  });
});

describe('HistoryStore: branch switching verified against the persistent tree', () => {
  it('commits follow the active branch; snapshots prove each step', () => {
    const store = makeStore();
    store.commit(1); // v2 on main
    store.commit(2); // v3 on main
    const snapAtMain = store.versionTree;

    store.createBranch('feature', store.head.id);
    store.switchBranch('feature');
    store.commit(10); // v4 on feature
    const snapOnFeature = store.versionTree;

    store.switchBranch('main');
    store.commit(3); // v5 on main, sibling of v4

    const tree = store.versionTree;
    const mainTip = tree.tipOf('main');
    const featureTip = tree.tipOf('feature');

    assert.equal(mainTip.state, 6);
    assert.equal(featureTip.state, 13);
    assert.equal(tree.node(mainTip.id).parentId, tree.node(featureTip.id).parentId,
      'diverged tips share the fork point as parent');
    assert.equal(tree.isAncestor(snapAtMain.tipOf('main').id, mainTip.id), true);
    assert.equal(tree.isAncestor(mainTip.id, featureTip.id), false, 'siblings, not ancestors');

    assert.equal(snapAtMain.size, 3, 'snapshot before branching unchanged');
    assert.equal(snapAtMain.hasBranch('feature'), false);
    assert.equal(snapOnFeature.tipOf('main').id, snapAtMain.tipOf('main').id,
      'feature commits did not move main');
    assert.deepEqual(
      store.listBranches().map((b) => b.name).sort(),
      ['feature', 'main'],
    );
  });
});
