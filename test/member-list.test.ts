import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { MemberList } from '../src/member-list.ts';
import type { NodeMetadata } from '../src/types.ts';

const baseMeta = (overrides: Partial<NodeMetadata> = {}): NodeMetadata => ({
  zone: 'us-east',
  weight: 10,
  role: 'worker',
  labels: { env: 'prod' },
  attributes: {},
  ...overrides,
});

describe('MemberList 元数据版本仲裁', () => {
  it('本地更新元数据会单调递增版本号', () => {
    const ml = new MemberList('n1', '127.0.0.1:1', baseMeta());
    assert.equal(ml.get('n1')!.version, 1);
    ml.updateLocalMetadata({ weight: 80 });
    const record = ml.get('n1')!;
    assert.equal(record.version, 2);
    assert.equal(record.metadata.weight, 80);
    ml.updateLocalMetadata({ role: 'leader' });
    assert.equal(ml.get('n1')!.version, 3);
  });

  it('拒绝版本号更低的过期更新（防止旧值覆盖新值）', () => {
    const ml = new MemberList('n1', '127.0.0.1:1', baseMeta());
    ml.updateLocalMetadata({ weight: 80 }); // version 2
    const stale = {
      nodeId: 'n1', address: '127.0.0.1:1',
      metadata: baseMeta({ weight: 10 }),
      version: 1, heartbeat: 1, state: 'alive' as const,
    };
    const accepted = ml.applyRemote(stale);
    assert.equal(ml.get('n1')!.metadata.weight, 80);
    assert.equal(accepted, false);
  });

  it('接受版本号更高的远端更新', () => {
    const ml = new MemberList('n1', '127.0.0.1:1', baseMeta());
    const fresh = {
      nodeId: 'n2', address: '127.0.0.1:2',
      metadata: baseMeta({ weight: 42 }),
      version: 7, heartbeat: 3, state: 'alive' as const,
    };
    assert.equal(ml.applyRemote(fresh), true);
    assert.equal(ml.get('n2')!.metadata.weight, 42);
    assert.equal(ml.get('n2')!.version, 7);
  });

  it('同版本并发更新按规范化摘要确定性仲裁，全网收敛一致', () => {
    // 两个节点分别收到同一 nodeId 的同版本但内容冲突的更新（顺序不同）
    const alice = new MemberList('alice', '127.0.0.1:1', baseMeta());
    const bob = new MemberList('bob', '127.0.0.1:2', baseMeta());
    const updateX = {
      nodeId: 'n9', address: '127.0.0.1:9',
      metadata: baseMeta({ weight: 80, zone: 'us-east' }),
      version: 5, heartbeat: 1, state: 'alive' as const,
    };
    const updateY = {
      nodeId: 'n9', address: '127.0.0.1:9',
      metadata: baseMeta({ weight: 30, zone: 'eu-west' }),
      version: 5, heartbeat: 1, state: 'alive' as const,
    };
    alice.applyRemote(updateX);
    alice.applyRemote(updateY);
    bob.applyRemote(updateY);
    bob.applyRemote(updateX);
    // 不论到达顺序如何，双方收敛到同一元数据
    assert.deepEqual(alice.get('n9')!.metadata, bob.get('n9')!.metadata);
  });

  it('心跳过期后节点被标记 suspect 进而 dead，不再健康', () => {
    let tick = 1_000;
    const ml = new MemberList('n1', '127.0.0.1:1', baseMeta(), {
      now: () => tick, suspectAfterMs: 100, deadAfterMs: 500,
    });
    ml.applyRemote({
      nodeId: 'n2', address: '127.0.0.1:2',
      metadata: baseMeta(), version: 1, heartbeat: 1, state: 'alive',
    });
    assert.equal(ml.healthyRecords().length, 2);
    tick += 200; // 超过 suspectAfterMs
    ml.refreshHealth();
    assert.equal(ml.get('n2')!.state, 'suspect');
    assert.equal(ml.healthyRecords().length, 1);
    tick += 400; // 超过 deadAfterMs
    ml.refreshHealth();
    assert.equal(ml.get('n2')!.state, 'dead');
    assert.equal(ml.healthyRecords().length, 1);
  });
});
