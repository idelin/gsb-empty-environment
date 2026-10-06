import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { VersionTree } from '../src/version-tree.ts';
import { BranchExistsError, UnknownVersionError } from '../src/errors.ts';

const add = (state: number, event: number): number => state + event;

describe('VersionTree (persistent)', () => {
  it('append returns a new tree and never mutates prior snapshots', () => {
    const t1 = VersionTree.create<number, number>(0);
    const { tree: t2, node: n2 } = t1.append(t1.rootId, 'main', 10, add(0, 10));
    const { tree: t3, node: n3 } = t2.append(n2.id, 'main', 5, add(10, 5));

    assert.equal(t1.size, 1);
    assert.equal(t1.has(n2.id), false);
    assert.equal(t1.branches().length, 1);

    assert.equal(t2.size, 2);
    assert.equal(t2.has(n3.id), false);

    assert.equal(t3.size, 3);
    assert.equal(t3.node(n3.id).state, 15);
    assert.equal(t3.node(n3.id).parentId, n2.id);
  });

  it('forkBranch adds a branch pointer without touching other tips', () => {
    const t1 = VersionTree.create<number, number>(0);
    const { tree: t2, node: n2 } = t1.append(t1.rootId, 'main', 1, 1);
    const t3 = t2.forkBranch('fix', n2.id);

    assert.equal(t2.hasBranch('fix'), false, 'snapshot before fork is unchanged');
    assert.equal(t3.tipOf('fix').id, n2.id);
    assert.equal(t3.tipOf('main').id, n2.id);

    const { tree: t4, node: n4 } = t3.append(n2.id, 'fix', 100, 101);
    assert.equal(t4.tipOf('fix').id, n4.id);
    assert.equal(t4.tipOf('main').id, n2.id, 'main tip not overwritten');
    assert.equal(t4.node(n4.id).branch, 'fix');
  });

  it('tracks topology: children, path, ancestry across branches', () => {
    let tree = VersionTree.create<number, number>(0);
    const root = tree.rootId;
    const a = tree.append(root, 'main', 1, 1);
    const b = a.tree.append(a.node.id, 'main', 1, 2);
    tree = b.tree.forkBranch('fix', a.node.id);
    const c = tree.append(a.node.id, 'fix', 10, 11);
    tree = c.tree;

    assert.deepEqual(
      tree.childrenOf(a.node.id).map((n) => n.id).sort(),
      [b.node.id, c.node.id].sort(),
    );
    assert.deepEqual(
      tree.pathTo(c.node.id).map((n) => n.id),
      [root, a.node.id, c.node.id],
    );
    assert.equal(tree.isAncestor(a.node.id, c.node.id), true);
    assert.equal(tree.isAncestor(b.node.id, c.node.id), false);
    assert.equal(tree.isAncestor(root, b.node.id), true);
  });

  it('rejects unknown parents, unknown versions and duplicate branches', () => {
    const tree = VersionTree.create<number, number>(0);
    assert.throws(() => tree.append('nope', 'main', 1, 1), UnknownVersionError);
    assert.throws(() => tree.node('nope'), UnknownVersionError);
    assert.throws(() => tree.forkBranch('main', tree.rootId), BranchExistsError);
    assert.throws(() => tree.forkBranch('fix', 'nope'), UnknownVersionError);
  });
});
