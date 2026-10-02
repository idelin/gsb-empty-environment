import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Cluster } from '../src/cluster.ts';
import { GossipNode } from '../src/node.ts';

const N = 100;
const LOG2_N = Math.ceil(Math.log2(N));

describe('piggyback gossip convergence (100 nodes)', () => {
  it('join event reaches all 100 nodes within log2(N) periods', () => {
    const cluster = new Cluster(N, 42);
    const fresh = new GossipNode('node-100', N + 1);
    cluster.join(fresh, 0);

    assert.equal(cluster.countAware('node-100'), 1);

    let convergedAt = -1;
    for (let p = 1; p <= LOG2_N; p++) {
      cluster.runPeriod();
      if (cluster.countAware('node-100') === N) {
        convergedAt = p;
        break;
      }
    }

    assert.ok(
      convergedAt > 0,
      `not converged after ${LOG2_N} periods (aware=${cluster.countAware('node-100')})`,
    );
    assert.ok(convergedAt <= LOG2_N);
    for (const node of cluster.nodes) {
      assert.equal(node.table.statusOf('node-100'), 'alive');
    }
  });

  it('leave event also converges within log2(N) periods', () => {
    const cluster = new Cluster(N, 7);
    cluster.nodes[3].publish('leave', 'node-50', 0);
    let convergedAt = -1;
    for (let p = 1; p <= LOG2_N; p++) {
      cluster.runPeriod();
      if (cluster.countAware('node-50', 'dead') === N) {
        convergedAt = p;
        break;
      }
    }
    assert.ok(convergedAt > 0 && convergedAt <= LOG2_N);
  });

  it('suspect event converges and alive refutes it cluster-wide', () => {
    const cluster = new Cluster(N, 99);
    cluster.nodes[0].publish('suspect', 'node-10', 0);
    for (let p = 1; p <= LOG2_N; p++) cluster.runPeriod();
    assert.equal(cluster.countAware('node-10', 'suspect'), N);

    cluster.nodes[1].publish('alive', 'node-10', 1);
    for (let p = 1; p <= LOG2_N; p++) cluster.runPeriod();
    assert.equal(cluster.countAware('node-10', 'alive'), N);
  });
});
