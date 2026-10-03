import { ACCEPTANCE_CONFIG, runAcceptanceScenario } from './scenario.ts';

const sim = runAcceptanceScenario();
const cfg = ACCEPTANCE_CONFIG;

console.log('=== 主备租约协商状态机 · 验收场景事件流(离散 tick)===');
console.log(
  `配置: nodes=[${cfg.nodeIds.join(',')}] majority=${sim.majority} ttl=${cfg.leaseTtl} ` +
    `maxSkew=${cfg.maxClockSkew} netDelay=${cfg.networkDelay} hbInterval=${cfg.heartbeatInterval}`,
);
console.log('');

for (const e of sim.events) {
  const tick = String(e.tick).padStart(3, '0');
  const node = (e.node ?? 'sim').padEnd(3);
  console.log(`[tick ${tick}] ${node} ${e.type.padEnd(18)} ${e.detail}`);
}

console.log('');
console.log('=== 终态 ===');
for (const node of sim.nodes.values()) {
  const writes = node.writes.map((w) => `${w.value}@tick${w.tick}`).join(', ');
  console.log(
    `${node.id}: role=${node.role} term=${node.term} writes=[${writes}]` +
      (node.lease ? ` lease(holder=${node.lease.holder},expiryLocal=${node.lease.expiryLocal})` : ''),
  );
}
