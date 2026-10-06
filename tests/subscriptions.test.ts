import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { HistoryStore } from '../src/history-store.ts';
import { UnknownBranchError, ViewClosedError } from '../src/errors.ts';

const add = (state: number, event: number): number => state + event;
const makeStore = () => new HistoryStore<number, number>(add, 0);

describe('branch subscriptions', () => {
  it('fires only for its branch and stops after unsubscribe', () => {
    const store = makeStore();
    const seen: string[] = [];
    const offMain = store.subscribeBranch('main', (node, branch) =>
      seen.push(`${branch}:${node.id}`),
    );

    store.commit(1);
    store.createBranch('fix', store.head.id);
    store.switchBranch('fix');
    store.subscribeBranch('fix', (node, branch) => seen.push(`${branch}:${node.id}`));
    store.commit(2);

    assert.deepEqual(seen, ['main:v2', 'fix:v3']);

    offMain();
    store.switchBranch('main');
    store.commit(3);
    assert.deepEqual(seen, ['main:v2', 'fix:v3'], 'unsubscribed listener not called');
    assert.equal(store.subscriptionCount, 1);
  });

  it('rejects subscriptions to unknown branches', () => {
    const store = makeStore();
    assert.throws(() => store.subscribeBranch('nope', () => {}), UnknownBranchError);
  });
});

describe('view-scoped subscriptions and cleanup', () => {
  it('onChild observes commits appended directly on the viewed version', () => {
    const store = makeStore();
    const view = store.checkout(store.head.id);
    const children: string[] = [];
    view.onChild((child) => children.push(child.id));

    store.commit(1);
    store.commit(2);

    assert.deepEqual(children, ['v2'], 'only direct children of the viewed version');
    assert.equal(store.subscriptionCount, 1);
  });

  it('switching views cleans up the previous view\'s subscriptions', () => {
    const store = makeStore();
    store.commit(1); // v2
    store.commit(2); // v3

    const oldView = store.checkout('v2');
    const calls: string[] = [];
    oldView.onChild((child) => calls.push(child.id));
    assert.equal(store.subscriptionCount, 1);

    const newView = store.checkout('v3');
    assert.equal(oldView.closed, true, 'previous view closed by checkout');
    assert.equal(store.subscriptionCount, 0, 'old subscriptions released');
    assert.equal(store.activeViewSession, newView);

    store.createBranch('fix', 'v2'); // would have notified the old listener
    store.switchBranch('fix');
    store.commit(100); // child of v2 on branch fix
    assert.deepEqual(calls, [], 'stale listener never fired');
  });

  it('close() releases subscriptions and disables the view', () => {
    const store = makeStore();
    const view = store.checkout(store.head.id);
    const calls: number[] = [];
    const off = view.onChild(() => calls.push(1));
    off();
    view.onChild(() => calls.push(2));
    assert.equal(store.subscriptionCount, 1);

    view.close();
    assert.equal(view.closed, true);
    assert.equal(store.subscriptionCount, 0, 'all view resources cleaned up');
    assert.equal(store.activeViewSession, null);

    store.commit(5);
    assert.deepEqual(calls, [], 'no listener fired after close');
    assert.throws(() => view.onChild(() => {}), ViewClosedError);
    assert.throws(() => view.state, ViewClosedError);
  });

  it('fork() closes the view and cleans up its subscriptions', () => {
    const store = makeStore();
    store.commit(1);
    const view = store.checkout('v2');
    view.onChild(() => {});
    assert.equal(store.subscriptionCount, 1);

    view.fork('fix');
    assert.equal(view.closed, true);
    assert.equal(store.subscriptionCount, 0);
    assert.equal(store.activeViewSession, null);
    assert.equal(store.activeBranchName, 'fix');
  });

  it('persistent tree snapshots verify subscription-era history after cleanup', () => {
    const store = makeStore();
    store.commit(1);
    const snapshot = store.versionTree;

    const view = store.checkout('v2');
    view.onChild(() => {});
    view.close();
    store.commit(2);

    assert.equal(store.subscriptionCount, 0);
    assert.equal(snapshot.size, 2, 'snapshot from view era is immutable');
    assert.equal(snapshot.tipOf('main').id, 'v2');
    assert.equal(store.versionTree.tipOf('main').id, 'v3');
  });
});
