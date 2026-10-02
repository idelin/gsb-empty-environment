import test from 'node:test';
import assert from 'node:assert/strict';
import {
  runAcceptanceScenario,
  runRenewalScenario,
} from '../src/scenario.ts';
import { DEFAULT_CONFIG } from '../src/config.ts';

const sim = runAcceptanceScenario();

test('tick 100：主节点发起租约申请', () => {
  const e = sim.find('campaign', (x) => x.tick === 100 && x.node === 'A');
  assert.ok(e, 'A 应在 tick 100 发起竞选');
  assert.match(e.detail, /epoch 1/);
  assert.match(e.detail, /requesting lease \[105, 150\)/);
});

test('tick 105：多数派确认，A 获得租约 [105, 150)', () => {
  const e = sim.find('lease-acquired', (x) => x.tick === 105 && x.node === 'A');
  assert.ok(e, 'A 应在 tick 105 获得多数派确认');
  assert.match(e.detail, /majority \{A,B\}/);
  assert.match(e.detail, /lease \[105, 150\)/);
  // B、C 在 102 授予投票并承诺至 150
  assert.equal(sim.filter('vote-granted', (x) => x.tick === 102).length, 2);
});

test('网络黑洞期间：A 的心跳与续期全部丢失，无 lease-renewed', () => {
  const drops = sim.filter(
    'message-dropped',
    (x) => x.tick >= 106 && x.tick < 153,
  );
  assert.ok(drops.length > 0, '黑洞期间应有丢包事件');
  assert.equal(
    sim.find('lease-renewed', (x) => x.node === 'A'),
    undefined,
    '黑洞中 A 不应获得多数派续期',
  );
});

test('tick 150：旧主 A 租约到期自动降级为候选者', () => {
  const e = sim.find('demoted', (x) => x.tick === 150 && x.node === 'A');
  assert.ok(e, 'A 应在 tick 150 主动降级');
  assert.match(e.detail, /lease expired at 150 without majority renewal/);
  const recampaign = sim.find(
    'campaign',
    (x) => x.tick === 150 && x.node === 'A',
  );
  assert.ok(recampaign, 'A 降级后应以候选者身份重新竞选');
  assert.match(recampaign.detail, /epoch 2/);
});

test('tick 155：B 由多数派选为新主，租约 [155, 200)', () => {
  const e = sim.find('lease-acquired', (x) => x.tick === 155 && x.node === 'B');
  assert.ok(e, 'B 应在 tick 155 获得多数派确认');
  assert.match(e.detail, /majority \{B,C\}/);
  assert.match(e.detail, /lease \[155, 200\)/);
});

test('tick 155 之后：新主与多数派拒绝旧主 A 的请求', () => {
  const rejections = sim.filter(
    'request-rejected',
    (x) => x.tick >= 155 && /from A/.test(x.detail),
  );
  assert.ok(rejections.length >= 2, 'B 和 C 都应拒绝 A 的过期请求');
  assert.ok(rejections.some((x) => x.node === 'B'));
  assert.ok(rejections.some((x) => x.node === 'C'));
  // A 最终承认新主，降级为 follower
  const stepDown = sim.find(
    'step-down',
    (x) => x.node === 'A' && /recognized primary B/.test(x.detail),
  );
  assert.ok(stepDown, 'A 应承认新主 B 并降为 follower');
  assert.equal(sim.nodes.get('A')!.role, 'follower');
});

test('写栅栏：时钟漂移受界，租约边界处拒绝写入', () => {
  const maxOffset = Math.max(
    ...Object.values(DEFAULT_CONFIG.clockOffsets).map(Math.abs),
  );
  assert.ok(
    maxOffset <= DEFAULT_CONFIG.maxClockDrift,
    '所有节点时钟偏移须在漂移界内',
  );
  // 147 < 150 - 2：栅栏内，可写
  assert.ok(sim.find('write-accepted', (x) => x.tick === 147 && x.node === 'A'));
  // 149 >= 150 - 2：进入漂移栅栏，旧主拒绝写
  const fenced = sim.find('write-rejected', (x) => x.tick === 149 && x.node === 'A');
  assert.ok(fenced);
  assert.match(fenced.detail, /drift fence/);
  // 156：A 已非主，拒绝写；B 为新主，接受写
  const staleWrite = sim.find(
    'write-rejected',
    (x) => x.tick === 156 && x.node === 'A',
  );
  assert.ok(staleWrite);
  assert.match(staleWrite.detail, /not primary/);
  assert.ok(sim.find('write-accepted', (x) => x.tick === 156 && x.node === 'B'));
});

test('不变量：任意 tick 至多一个具备写能力的主节点（无双主脑裂写）', () => {
  for (const { tick, primaries } of sim.writeCapableTimeline) {
    assert.ok(
      primaries.length <= 1,
      `tick ${tick} 出现多个可写主节点: ${primaries.join(',')}`,
    );
  }
});

test('对照场景：多数派心跳应答驱动租约续期', () => {
  const healthy = runRenewalScenario();
  const renewals = healthy.filter('lease-renewed', (x) => x.node === 'A');
  assert.ok(renewals.length >= 3, '无黑洞时 A 应持续续期');
  const nodeA = healthy.nodes.get('A')!;
  assert.equal(nodeA.role, 'primary');
  assert.ok(nodeA.leaseExpiry > 150, '租约应被续期到 150 之后');
  // 无人挑战主节点：除 tick 100 外无竞选
  assert.equal(
    healthy.filter('campaign', (x) => x.tick > 100).length,
    0,
  );
});
