import { KeyRegistry } from '../src/crypto.ts';
import { DelegationLedger } from '../src/ledger.ts';
import type { Condition } from '../src/types.ts';

const T0 = 1_700_000_000;
const HOUR = 3_600;
const DAY = 24 * HOUR;

const show = (title: string, value: unknown): void => {
  console.log(`\n=== ${title} ===`);
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
};

// 1. Local keys only — no external identity service is ever contacted.
const keys = new KeyRegistry();
for (const principal of ['root', 'security-admin', 'team-lead', 'analyst']) {
  keys.generate(principal);
}
const ledger = new DelegationLedger(keys);

// 2. Root grants the security admin a wide, delegatable scope for 30 days.
const adminGrant = ledger.issueRoot({
  subject: 'security-admin',
  resources: ['db/customers', 'db/orders', 'db/audit-log'],
  actions: ['read', 'write', 'export'],
  maxDepth: 3,
  delegatable: true,
  notBefore: T0,
  expiresAt: T0 + 30 * DAY,
});

// 3. Admin narrows scope and depth for the team lead (7 days, depth 2).
const leadGrant = ledger.delegate({
  parentId: adminGrant.id,
  subject: 'team-lead',
  resources: ['db/customers', 'db/orders'],
  actions: ['read', 'write'],
  maxDepth: 2,
  delegatable: true,
  notBefore: T0,
  expiresAt: T0 + 7 * DAY,
  now: T0,
});

// 4. Team lead issues a read-only, non-delegatable, 1-day grant to an
//    analyst, guarded by an attribute policy (finite set + integer check).
const analystPolicy: Condition = {
  op: 'and',
  conditions: [
    { op: 'in', attr: 'department', set: ['analytics', 'ops'] },
    { op: 'gte', attr: 'clearanceLevel', value: 2 },
    { op: 'not', condition: { op: 'in', attr: 'employmentStatus', set: ['terminated'] } },
  ],
};
const analystGrant = ledger.delegate({
  parentId: leadGrant.id,
  subject: 'analyst',
  resources: ['db/customers'],
  actions: ['read'],
  policy: analystPolicy,
  maxDepth: 0,
  delegatable: false,
  notBefore: T0,
  expiresAt: T0 + DAY,
  now: T0,
});

show('Delegation chain (root -> leaf)', [
  `${'root'} -> security-admin  (depth 3, 30d, [read,write,export] on 3 resources)`,
  `security-admin -> team-lead (depth 2, 7d,  [read,write] on 2 resources)`,
  `team-lead -> analyst        (depth 0, 1d,  [read] on db/customers, non-delegatable, policy-bound)`,
]);

// 5. Authorized access: valid chain + in-scope + attributes satisfy policy.
const granted = ledger.authorize({
  tokenId: analystGrant.id,
  resource: 'db/customers',
  action: 'read',
  attributes: { department: 'analytics', clearanceLevel: 3, employmentStatus: 'active' },
  now: T0 + 4 * HOUR,
});
show('Authorize analyst read db/customers (allowed)', {
  allowed: granted.allowed,
  reason: granted.reason,
  policyTrace: granted.proof.policyTrace,
});

// 6. Full chain proof for the granted access.
show('Full chain proof (root first)', {
  tokenId: granted.proof.tokenId,
  verifiedAt: granted.proof.verifiedAt,
  valid: granted.proof.valid,
  chain: granted.proof.chain.map((link) => ({
    tokenId: link.tokenId,
    issuer: link.issuer,
    subject: link.subject,
    resources: link.resources,
    actions: link.actions,
    window: [link.notBefore, link.expiresAt],
    checks: link.checks.map((c) => `${c.ok ? 'PASS' : 'FAIL'} ${c.name}`),
  })),
});

// 7. Denials: out-of-scope action and policy-rejecting attributes.
show(
  'Deny: analyst tries to write',
  ledger.authorize({
    tokenId: analystGrant.id,
    resource: 'db/customers',
    action: 'write',
    attributes: { department: 'analytics', clearanceLevel: 3 },
    now: T0 + 4 * HOUR,
  }).reason,
);
show(
  'Deny: clearance too low',
  ledger.authorize({
    tokenId: analystGrant.id,
    resource: 'db/customers',
    action: 'read',
    attributes: { department: 'analytics', clearanceLevel: 1 },
    now: T0 + 4 * HOUR,
  }).reason,
);

// 8. Attenuation rules reject power growth at issuance time.
const expectReject = (label: string, fn: () => unknown): void => {
  try {
    fn();
    show(label, 'UNEXPECTEDLY ACCEPTED');
  } catch (error) {
    show(label, `rejected: ${(error as Error).message}`);
  }
};
expectReject('Reject: widening resource scope', () =>
  ledger.delegate({
    parentId: leadGrant.id,
    subject: 'intern',
    resources: ['db/customers', 'db/audit-log'],
    actions: ['read'],
    maxDepth: 1,
    delegatable: false,
    notBefore: T0,
    expiresAt: T0 + DAY,
    now: T0,
  }),
);
expectReject('Reject: delegating a non-delegatable grant', () =>
  ledger.delegate({
    parentId: analystGrant.id,
    subject: 'intern',
    resources: ['db/customers'],
    actions: ['read'],
    maxDepth: 0,
    delegatable: false,
    notBefore: T0,
    expiresAt: T0 + HOUR,
    now: T0,
  }),
);
expectReject('Reject: delegation cycle back to security-admin', () =>
  ledger.delegate({
    parentId: leadGrant.id,
    subject: 'security-admin',
    resources: ['db/customers'],
    actions: ['read'],
    maxDepth: 1,
    delegatable: false,
    notBefore: T0,
    expiresAt: T0 + DAY,
    now: T0,
  }),
);

// 9. Expiry cascades: after the team-lead grant expires, the analyst grant
//    is dead too, even though checked against the chain, not its own clock.
const afterLeadExpiry = ledger.verifyChain(analystGrant.id, T0 + 8 * DAY);
show('After team-lead grant expires (day 8)', {
  valid: afterLeadExpiry.valid,
  failure: afterLeadExpiry.failure,
});

// 10. Revoking an ancestor invalidates every descendant.
ledger.revoke(leadGrant.id);
const afterRevoke = ledger.verifyChain(analystGrant.id, T0 + 4 * HOUR);
show('After revoking the team-lead grant', {
  valid: afterRevoke.valid,
  failure: afterRevoke.failure,
});
