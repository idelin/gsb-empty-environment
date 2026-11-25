import test from 'node:test';
import assert from 'node:assert/strict';

import {
  ALL_DISCLOSABLE_FIELDS,
  DeliveryOutbox,
  ReportingHost,
  buildReportPlan,
  createCompressionDictionary,
  createSamplingCommitment,
  decodeReportWithDictionary,
  ingestEvidence,
  verifyReportForDelivery,
  verifySnapshotIntegrity,
  toWireReport,
  verifySamplingCommitment,
  type CandidateReport,
  type EvidenceEvent,
  type FailureEvidence,
  type PolicySpec,
} from '../src/index.ts';

const ENDPOINT = {
  endpointId: 'ep-a',
  groupId: 'group-a',
  url: 'https://collector.example/reports',
} as const;

function failure(
  id: string,
  overrides: Partial<FailureEvidence> = {},
): FailureEvidence {
  return {
    id,
    kind: 'failure',
    occurredAt: 100,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    policySourceId: 'policy-v1',
    lineageId: 'line-a',
    topLevelSite: 'shop.example',
    documentHost: 'shop.example',
    documentGeneration: 'gen-1',
    endpoint: ENDPOINT,
    bucketKey: 'checkout',
    statusCode: 503,
    allowedFields: ALL_DISCLOSABLE_FIELDS,
    sampling: createSamplingCommitment(id, 1),
    ...overrides,
  };
}

function policyEvent(
  policyOverrides: Partial<PolicySpec> = {},
  eventOverrides: Partial<EvidenceEvent> = {},
): EvidenceEvent {
  const policy: PolicySpec = {
    policyId: 'policy-v1',
    lineageId: 'line-a',
    principalId: 'principal-a',
    tenantId: 'tenant-a',
    enabled: true,
    allowedFields: ALL_DISCLOSABLE_FIELDS,
    topLevelSites: ['shop.example'],
    endpoints: [ENDPOINT],
    batchMaxContributions: 2,
    ...policyOverrides,
  };
  return {
    id: 'policy-1',
    kind: 'policy-replaced',
    occurredAt: 1,
    tenantId: policy.tenantId,
    policy,
    causalPrefix: [],
    ...eventOverrides,
  } as EvidenceEvent;
}

function ghostFailure(id = 'hidden-ghost'): FailureEvidence {
  return failure(id, {
    principalId: 'principal-ghost',
    policySourceId: 'missing-policy',
    lineageId: 'line-ghost',
  });
}

interface DeliveredSequence {
  readonly wires: readonly any[];
  readonly payloads: readonly string[];
  readonly reportIds: readonly string[];
  readonly batchIds: readonly string[];
  readonly deliveryIds: readonly string[];
  readonly candidates: readonly CandidateReport[];
  readonly snapshot: ReturnType<typeof ingestEvidence>['snapshot'];
}

function runDeliveredSequence(
  events: readonly EvidenceEvent[],
  options: {
    readonly dictionary?: ReturnType<typeof createCompressionDictionary>;
  } = {},
): DeliveredSequence {
  let snapshot = null;
  const outbox = new DeliveryOutbox();
  const seenReportIds = new Set<string>();
  const wires: any[] = [];
  const payloads: string[] = [];
  const reportIds: string[] = [];
  const batchIds: string[] = [];
  const deliveryIds: string[] = [];

  let result = ingestEvidence(snapshot, [], { dictionary: options.dictionary });
  for (const event of events) {
    result = ingestEvidence(result.snapshot, event, {
      createdAt: 1,
      dictionary: options.dictionary,
    });
    snapshot = result.snapshot;

    for (const candidate of result.candidates) {
      if (candidate.state !== 'sealed' || seenReportIds.has(candidate.reportId)) {
        continue;
      }
      seenReportIds.add(candidate.reportId);
      const approval = outbox.approve({
        candidate,
        snapshot: result.snapshot,
        dictionary: options.dictionary,
      });
      if (!approval.ok) {
        assert.fail(`candidate was not verifiable: ${JSON.stringify(approval.verification)}`);
      }
      const attempt = outbox.beginAttempt(approval.delivery.deliveryId, result.snapshot);
      if (!attempt.ok) {
        assert.fail(`delivery could not start: ${attempt.reason}`);
      }
      wires.push(JSON.parse(attempt.payload));
      payloads.push(attempt.payload);
      reportIds.push(candidate.reportId);
      batchIds.push(candidate.batchId);
      deliveryIds.push(approval.delivery.deliveryId);
      outbox.acknowledge(approval.delivery.deliveryId, 100);
    }
  }

  return {
    wires,
    payloads,
    reportIds,
    batchIds,
    deliveryIds,
    candidates: result.candidates,
    snapshot,
  };
}

test('aggregates only permitted fields and records provenance', () => {
  const events = [
    policyEvent({ batchMaxContributions: 10 }),
    failure('f-1', { occurredAt: 120, statusCode: 500 }),
    failure('f-2', { occurredAt: 90, statusCode: 503 }),
  ];
  const result = ingestEvidence(null, events, { createdAt: 1 });
  assert.equal(result.candidates.length, 1);
  const candidate = result.candidates[0];
  assert.equal(candidate.state, 'open');
  assert.equal(candidate.buckets.length, 1);
  assert.deepEqual(candidate.buckets[0].statusCodes, [500, 503]);
  assert.equal(candidate.buckets[0].count, 2);
  assert.equal(candidate.buckets[0].earliestTimestamp, 90);
  assert.deepEqual(candidate.proof.contributorIds, ['f-1', 'f-2']);

  const verification = verifyReportForDelivery({
    report: candidate,
    snapshot: result.snapshot,
  });
  assert.equal(verification.status, 'verified');
});

test('field-level disclosure excludes only the unauthorized status contribution', () => {
  const events = [
    policyEvent({ batchMaxContributions: 10 }),
    failure('f-secret-status', {
      statusCode: 500,
      allowedFields: ALL_DISCLOSABLE_FIELDS.filter((field) => field !== 'statusCodes'),
    }),
    failure('f-public-status', { statusCode: 404 }),
  ];
  const { snapshot, candidates } = ingestEvidence(null, events);
  const bucket = candidates[0].buckets[0];
  assert.equal(bucket.count, 2);
  assert.deepEqual(bucket.statusCodes, [404]);
  assert.deepEqual(candidates[0].proof.statusCodeEventIds, {
    404: ['f-public-status'],
  });
  assert.equal(
    verifyReportForDelivery({ report: candidates[0], snapshot }).status,
    'verified',
  );
});

test('paired histories differing only by an unauthorized event have identical report sequences', () => {
  const dictionary = createCompressionDictionary('paired-dict', [
    'count',
    'earliestTimestamp',
    'statusCodes',
  ]);
  const base = [
    policyEvent(),
    failure('visible-1', { occurredAt: 101, statusCode: 500 }),
    failure('visible-2', { occurredAt: 102, statusCode: 502 }),
    failure('visible-3', { occurredAt: 103, statusCode: 503 }),
    failure('visible-4', { occurredAt: 104, statusCode: 504 }),
  ];

  for (let position = 0; position <= base.length; position += 1) {
    const hidden = ghostFailure(`ghost-${position}`);
    (hidden as { pageUrl?: string }).pageUrl =
      `https://visited-secret.example/private-page-${position}`;
    const withHidden = [...base.slice(0, position), hidden, ...base.slice(position)];
    const clean = runDeliveredSequence(base, { dictionary });
    const polluted = runDeliveredSequence(withHidden, { dictionary });

    assert.equal(clean.wires.length, 2);
    assert.deepEqual(polluted.wires, clean.wires, `wire mismatch at position ${position}`);
    assert.deepEqual(polluted.payloads, clean.payloads, `payload mismatch at ${position}`);
    assert.deepEqual(polluted.reportIds, clean.reportIds);
    assert.deepEqual(polluted.batchIds, clean.batchIds);
    assert.deepEqual(polluted.deliveryIds, clean.deliveryIds);
    assert.ok(
      polluted.payloads.every(
        (payload) => !payload.includes('visited-secret.example'),
      ),
    );
    assert.deepEqual(
      polluted.candidates.map((candidate) => candidate.encoded?.payload),
      clean.candidates.map((candidate) => candidate.encoded?.payload),
    );
  }
});

test('hidden first event cannot consume a full quota or move batch boundaries', () => {
  const policy = policyEvent({ batchMaxContributions: 1 });
  const visible = [
    policy,
    failure('quota-1'),
    failure('quota-2'),
    failure('quota-3'),
  ];

  for (const hiddenPosition of [0, 1, 2, 3]) {
    const hidden = ghostFailure(`quota-hidden-${hiddenPosition}`);
    const pollutedEvents = [
      ...visible.slice(0, hiddenPosition),
      hidden,
      ...visible.slice(hiddenPosition),
    ];
    const clean = runDeliveredSequence(visible);
    const polluted = runDeliveredSequence(pollutedEvents);
    assert.deepEqual(polluted.wires, clean.wires);
    assert.deepEqual(
      polluted.wires.map((report: any) => report.batchIndex),
      [0, 1, 2],
    );
    assert.deepEqual(
      polluted.wires.map((report: any) => report.buckets[0].count),
      [1, 1, 1],
    );
  }
});

test('paired sequences match for every authorization failure class', () => {
  const hiddenEndpoint = {
    endpointId: 'ep-hidden',
    groupId: 'group-hidden',
    url: 'https://collector.example/hidden',
  };
  const cases: ReadonlyArray<{
    name: string;
    common?: readonly EvidenceEvent[];
    policy?: EvidenceEvent;
    hidden: FailureEvidence;
  }> = [
    {
      name: 'missing batch boundary permission',
      hidden: failure('hidden-no-batch', {
        allowedFields: ALL_DISCLOSABLE_FIELDS.filter((field) => field !== 'batchBoundary'),
      }),
    },
    {
      name: 'excluded by sampling promise',
      hidden: failure('hidden-unsampled', {
        sampling: createSamplingCommitment('hidden-unsampled', 0),
      }),
    },
    {
      name: 'generation revocation',
      common: [
        {
          id: 'revoke-hidden-generation',
          kind: 'report-permission-revoked',
          occurredAt: 20,
          tenantId: 'tenant-a',
          principalId: 'principal-a',
          documentGeneration: 'gen-hidden',
        },
      ],
      hidden: failure('hidden-generation', { documentGeneration: 'gen-hidden' }),
    },
    {
      name: 'endpoint group revocation',
      policy: policyEvent({ endpoints: [ENDPOINT, hiddenEndpoint] }),
      common: [
        {
          id: 'revoke-hidden-group',
          kind: 'endpoint-group-revoked',
          occurredAt: 20,
          tenantId: 'tenant-a',
          principalId: 'principal-a',
          endpointGroupId: hiddenEndpoint.groupId,
        },
      ],
      hidden: failure('hidden-group', { endpoint: hiddenEndpoint }),
    },
    {
      name: 'unknown policy principal',
      hidden: ghostFailure('hidden-unknown'),
    },
  ];

  for (const testCase of cases) {
    const policy = testCase.policy ?? policyEvent();
    const common = testCase.common ?? [];
    const visible = [failure(`${testCase.name}-visible-1`), failure(`${testCase.name}-visible-2`)];
    const clean = runDeliveredSequence([policy, ...common, ...visible]);
    const polluted = runDeliveredSequence([
      policy,
      ...common,
      testCase.hidden,
      ...visible,
    ]);
    assert.equal(clean.wires.length, 1, testCase.name);
    assert.deepEqual(polluted.payloads, clean.payloads, testCase.name);
    assert.deepEqual(polluted.deliveryIds, clean.deliveryIds, testCase.name);
  }
});

test('retry reuses the sealed payload until a new revocation becomes visible', () => {
  const policy = policyEvent({ batchMaxContributions: 1 });
  const event = failure('retry-1', { documentGeneration: 'gen-old' });
  const first = ingestEvidence(null, [policy, event]);
  const candidate = first.candidates[0];
  assert.equal(candidate.state, 'sealed');

  const outbox = new DeliveryOutbox();
  const approval = outbox.approve({ candidate, snapshot: first.snapshot });
  assert.equal(approval.ok, true);
  if (!approval.ok) {
    throw new Error('approval failed');
  }
  const deliveryId = approval.delivery.deliveryId;
  const firstAttempt = outbox.beginAttempt(deliveryId, first.snapshot);
  assert.equal(firstAttempt.ok, true);
  if (!firstAttempt.ok) {
    throw new Error(firstAttempt.reason);
  }
  const originalPayload = firstAttempt.payload;
  outbox.failAttempt(deliveryId, 'network offline');

  const retry = outbox.beginAttempt(deliveryId, first.snapshot);
  assert.equal(retry.ok, true);
  if (retry.ok) {
    assert.equal(retry.payload, originalPayload);
    assert.equal(retry.attempt, 2);
  }
  outbox.failAttempt(deliveryId, 'network offline again');

  const revocation: EvidenceEvent = {
    id: 'revoke-old-gen',
    kind: 'report-permission-revoked',
    occurredAt: 200,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    documentGeneration: 'gen-old',
    causalPrefix: ['policy-1'],
  };
  const revoked = ingestEvidence(first.snapshot, revocation);
  outbox.observeSnapshot(revoked.snapshot);
  const blocked = outbox.beginAttempt(deliveryId, revoked.snapshot);
  assert.equal(blocked.ok, false);
  if (!blocked.ok) {
    assert.equal(blocked.status, 'suppressed');
  }
  const record = outbox.get(deliveryId);
  assert.equal(record?.payload, originalPayload);
  assert.equal(record?.attempts, 2);

  const emptyAfterRevocation = buildReportPlan(revoked.snapshot).candidates;
  const emptyHistory = ingestEvidence(null, [policy, revocation]).candidates;
  assert.deepEqual(emptyAfterRevocation.map((c) => c.reportId), []);
  assert.deepEqual(emptyHistory.map((c) => c.reportId), []);
});

test('revocation and acknowledgement interleavings never allow another publish', () => {
  function setup() {
    const snapshot = ingestEvidence(null, [
      policyEvent({ batchMaxContributions: 1 }),
      failure('ack-1'),
    ]).snapshot;
    const candidate = buildReportPlan(snapshot).candidates[0];
    const outbox = new DeliveryOutbox();
    const approval = outbox.approve({ candidate, snapshot });
    if (!approval.ok) {
      assert.fail('approval failed');
    }
    return { outbox, deliveryId: approval.delivery.deliveryId, snapshot };
  }

  const revocation: EvidenceEvent = {
    id: 'revoke-interleave',
    kind: 'report-permission-revoked',
    occurredAt: 200,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
  };

  {
    const { outbox, deliveryId, snapshot } = setup();
    assert.equal(outbox.beginAttempt(deliveryId, snapshot).ok, true);
    assert.equal(outbox.acknowledge(deliveryId, 50)?.status, 'acknowledged');
    const revoked = ingestEvidence(snapshot, revocation).snapshot;
    outbox.observeSnapshot(revoked);
    assert.equal(outbox.get(deliveryId)?.status, 'acknowledged');
    assert.equal(outbox.beginAttempt(deliveryId, revoked).ok, false);
    assert.equal(outbox.get(deliveryId)?.attempts, 1);
  }

  {
    const { outbox, deliveryId, snapshot } = setup();
    const revoked = ingestEvidence(snapshot, revocation).snapshot;
    outbox.observeSnapshot(revoked);
    assert.equal(outbox.get(deliveryId)?.status, 'suppressed');
    const attempt = outbox.beginAttempt(deliveryId, revoked);
    assert.equal(attempt.ok, false);
    assert.equal(outbox.get(deliveryId)?.attempts, 0);
    assert.equal(outbox.acknowledge(deliveryId)?.status, 'suppressed');
  }

  {
    const { outbox, deliveryId, snapshot } = setup();
    assert.equal(outbox.beginAttempt(deliveryId, snapshot).ok, true);
    const revoked = ingestEvidence(snapshot, revocation).snapshot;
    outbox.observeSnapshot(revoked);
    assert.equal(outbox.get(deliveryId)?.status, 'in_flight');
    assert.equal(outbox.get(deliveryId)?.revocationVisible, true);
    assert.equal(outbox.acknowledge(deliveryId, 70)?.status, 'acknowledged');
    assert.equal(outbox.beginAttempt(deliveryId, revoked).ok, false);
    assert.equal(outbox.get(deliveryId)?.attempts, 1);
  }

  {
    const { outbox, deliveryId, snapshot } = setup();
    assert.equal(outbox.beginAttempt(deliveryId, snapshot).ok, true);
    const revoked = ingestEvidence(snapshot, revocation).snapshot;
    outbox.observeSnapshot(revoked);
    assert.equal(outbox.failAttempt(deliveryId)?.status, 'suppressed');
    assert.equal(outbox.beginAttempt(deliveryId, revoked).ok, false);
    assert.equal(outbox.get(deliveryId)?.attempts, 1);
  }
});

test('same endpoint URL is reused by distinct policy principals without shared revocation', () => {
  const url = 'https://shared.example/collect';
  const sharedEndpoint = { endpointId: 'shared-edge', groupId: 'shared-group', url };
  const endpointA = sharedEndpoint;
  const endpointB = sharedEndpoint;
  const policyA = policyEvent(
    {
      policyId: 'policy-a',
      lineageId: 'line-a',
      principalId: 'principal-a',
      endpoints: [endpointA],
    },
    { id: 'policy-a-event' },
  );
  const policyB = policyEvent(
    {
      policyId: 'policy-b',
      lineageId: 'line-b',
      principalId: 'principal-b',
      endpoints: [endpointB],
    },
    { id: 'policy-b-event' },
  );
  const eventA = failure('endpoint-a', {
    principalId: 'principal-a',
    policySourceId: 'policy-a',
    lineageId: 'line-a',
    endpoint: endpointA,
  });
  const eventB = failure('endpoint-b', {
    principalId: 'principal-b',
    policySourceId: 'policy-b',
    lineageId: 'line-b',
    endpoint: endpointB,
  });

  const snapshot = ingestEvidence(null, [policyA, policyB, eventA, eventB]).snapshot;
  const candidates = buildReportPlan(snapshot).candidates;
  assert.equal(candidates.length, 2);
  assert.deepEqual(
    candidates.map((candidate) => candidate.principalId).sort(),
    ['principal-a', 'principal-b'],
  );

  const revocation: EvidenceEvent = {
    id: 'revoke-a-group',
    kind: 'endpoint-group-revoked',
    occurredAt: 20,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    endpointGroupId: 'shared-group',
  };
  const revokedSnapshot = ingestEvidence(snapshot, revocation).snapshot;
  const afterRevocation = buildReportPlan(revokedSnapshot).candidates;
  assert.equal(afterRevocation.length, 1);
  assert.equal(afterRevocation[0].principalId, 'principal-b');
  assert.equal(afterRevocation[0].endpointUrl, url);
  assert.equal(
    verifyReportForDelivery({ report: afterRevocation[0], snapshot: revokedSnapshot }).status,
    'verified',
  );
});

test('compression dictionary is shared across tenants without mixing tenant literals', () => {
  const dictionary = createCompressionDictionary('global-public-dict', [
    'count',
    'earliestTimestamp',
    'statusCodes',
    'present',
    'bucketKey',
  ]);

  function tenantHistory(tenant: string, bucket: string) {
    const endpoint = {
      endpointId: `ep-${tenant}`,
      groupId: `group-${tenant}`,
      url: `https://${tenant}.example/report`,
    };
    const policy = policyEvent(
      {
        tenantId: tenant,
        policyId: `policy-${tenant}`,
        lineageId: `line-${tenant}`,
        principalId: `principal-${tenant}`,
        topLevelSites: [`${tenant}.example`],
        endpoints: [endpoint],
      },
      { id: `policy-${tenant}-event`, tenantId: tenant },
    );
    const event = failure(`failure-${tenant}`, {
      tenantId: tenant,
      principalId: `principal-${tenant}`,
      policySourceId: `policy-${tenant}`,
      lineageId: `line-${tenant}`,
      topLevelSite: `${tenant}.example`,
      documentHost: `${tenant}.example`,
      endpoint,
      bucketKey: bucket,
    });
    return [policy, event] as const;
  }

  const historyA = tenantHistory('tenant-a', 'secret-a-bucket');
  const historyB = tenantHistory('tenant-b', 'secret-b-bucket');
  const snapshotA = ingestEvidence(null, historyA).snapshot;
  const snapshotB = ingestEvidence(null, historyB).snapshot;
  const combined = ingestEvidence(null, [...historyA, ...historyB]).snapshot;

  const candidateA = buildReportPlan(snapshotA, { dictionary }).candidates[0];
  const candidateB = buildReportPlan(snapshotB, { dictionary }).candidates[0];
  const combinedA = buildReportPlan(combined, { dictionary })
    .candidates.find((candidate) => candidate.tenantId === 'tenant-a');

  assert.ok(candidateA.encoded);
  assert.ok(candidateB.encoded);
  assert.equal(candidateA.encoded?.dictionaryId, dictionary.id);
  assert.equal(candidateB.encoded?.dictionaryHash, dictionary.hash);
  assert.equal(candidateA.encoded?.payload, combinedA?.encoded?.payload);
  assert.deepEqual(
    decodeReportWithDictionary(candidateA.encoded!, dictionary),
    toWireReport(candidateA),
  );
  assert.ok(candidateA.encoded?.payload.includes('secret-a-bucket'));
  assert.ok(!candidateA.encoded?.payload.includes('secret-b-bucket'));
  assert.ok(!candidateB.encoded?.payload.includes('secret-a-bucket'));

  const outbox = new DeliveryOutbox();
  const approval = outbox.approve({
    candidate: candidateA,
    snapshot: snapshotA,
    dictionary,
    useDictionaryPayload: true,
  });
  assert.equal(approval.ok, true);
  if (approval.ok) {
    const first = outbox.beginAttempt(approval.delivery.deliveryId, snapshotA);
    assert.equal(first.ok, true);
    if (first.ok) {
      assert.equal(first.payload, candidateA.encoded?.payload);
      assert.equal(first.contentType, 'application/gsb.dict-ref+json');
    }
    outbox.failAttempt(approval.delivery.deliveryId);
    const retry = outbox.beginAttempt(approval.delivery.deliveryId, snapshotA);
    assert.equal(retry.ok, true);
    if (retry.ok) {
      assert.equal(retry.payload, candidateA.encoded?.payload);
    }
  }
});

test('causal prefixes reconstruct out-of-order policy and revocation delivery', () => {
  const policy = policyEvent();
  const event = failure('causal-failure');
  const revocation: EvidenceEvent = {
    id: 'causal-revoke',
    kind: 'endpoint-group-revoked',
    occurredAt: 2,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    endpointGroupId: ENDPOINT.groupId,
    causalPrefix: ['policy-1'],
  };

  const early = ingestEvidence(null, revocation);
  assert.equal(early.pending.length, 1);
  assert.deepEqual(early.pending[0].missingEventIds, ['policy-1']);

  const afterPolicy = ingestEvidence(early.snapshot, policy);
  const afterEvent = ingestEvidence(afterPolicy.snapshot, event);
  assert.deepEqual(afterEvent.candidates, []);
  assert.equal(afterEvent.denied[0]?.reason, 'endpoint_group_revoked');

  const ordered = ingestEvidence(null, [policy, revocation, event]);
  assert.deepEqual(ordered.candidates, []);
});

test('policy replacement can arrive before its predecessor and converges', () => {
  const policyV1 = policyEvent();
  const policyV2 = policyEvent(
    { policyId: 'policy-v2' },
    { id: 'policy-2', causalPrefix: ['policy-1'] },
  );
  const event = failure('replacement-failure');

  const early = ingestEvidence(null, policyV2);
  assert.equal(early.pending.some((item) => item.eventId === 'policy-2'), true);
  const converged = ingestEvidence(early.snapshot, [policyV1, event]);
  const ordered = ingestEvidence(null, [policyV1, policyV2, event]);

  assert.equal(converged.candidates.length, 1);
  assert.equal(converged.candidates[0].reportId, ordered.candidates[0].reportId);
  assert.deepEqual(
    converged.candidates.map((candidate) => candidate.reportId),
    ordered.candidates.map((candidate) => candidate.reportId),
  );
});

test('subdomain inheritance controls policy scope', () => {
  const policy = policyEvent({
    sourceHost: 'parent.example',
    includeSubdomains: false,
  });
  const event = failure('subdomain-failure', {
    documentHost: 'child.parent.example',
  });
  const blocked = ingestEvidence(null, [policy, event]);
  assert.equal(blocked.candidates.length, 0);
  assert.equal(blocked.denied[0]?.reason, 'subdomain_not_in_scope');

  const inheritance: EvidenceEvent = {
    id: 'inherit-child',
    kind: 'subdomain-inheritance',
    occurredAt: 2,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    parentHost: 'parent.example',
    childHost: 'child.parent.example',
    enabled: true,
    causalPrefix: ['policy-1'],
  };
  const allowed = ingestEvidence(blocked.snapshot, inheritance);
  assert.equal(allowed.candidates.length, 1);
  assert.equal(
    verifyReportForDelivery({
      report: allowed.candidates[0],
      snapshot: allowed.snapshot,
    }).status,
    'verified',
  );
});

test('navigation generation boundaries and revocation are causal', () => {
  const policy = policyEvent({ batchMaxContributions: 10 });
  const oldEvent = failure('nav-old', { documentGeneration: 'gen-old' });
  const newEvent = failure('nav-new', { documentGeneration: 'gen-new' });
  const navigation: EvidenceEvent = {
    id: 'nav-1',
    kind: 'navigation',
    occurredAt: 50,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
    topLevelSite: 'shop.example',
    fromGeneration: 'gen-old',
    toGeneration: 'gen-new',
    revokePreviousReportPermission: true,
  };

  const revoked = ingestEvidence(null, [policy, oldEvent, newEvent, navigation]);
  assert.equal(revoked.candidates.length, 1);
  assert.equal(revoked.candidates[0].documentGeneration, 'gen-new');

  const retainedNavigation = { ...navigation, id: 'nav-2', revokePreviousReportPermission: false };
  const retained = ingestEvidence(null, [
    policy,
    oldEvent,
    newEvent,
    retainedNavigation,
  ]);
  assert.equal(retained.candidates.length, 2);
});

test('known missing event pages can only produce pending attestation', () => {
  const base = ingestEvidence(null, [
    policyEvent({ batchMaxContributions: 10 }),
    failure('missing-page-1'),
    failure('missing-page-2'),
  ]);
  const candidate = base.candidates[0];
  assert.ok(candidate);

  const missingSnapshot = ingestEvidence(base.snapshot, [], {
    knownMissingPages: [
      {
        pageId: 'page-7',
        scope: {
          tenantId: 'tenant-a',
          principalId: 'principal-a',
          endpointGroupId: 'group-a',
        },
      },
    ],
  }).snapshot;

  const result = verifyReportForDelivery({ report: candidate, snapshot: missingSnapshot });
  assert.equal(result.status, 'pending_attestation');
  if (result.status === 'pending_attestation') {
    assert.equal(result.missing[0]?.reference, 'page-7');
  }

  const tampered = JSON.parse(JSON.stringify(candidate)) as CandidateReport;
  (tampered.buckets[0] as { count: number }).count = 999;
  const whilePageMissing = verifyReportForDelivery({
    report: tampered,
    snapshot: missingSnapshot,
  });
  assert.equal(whilePageMissing.status, 'pending_attestation');

  const afterPageReturned = verifyReportForDelivery({
    report: tampered,
    snapshot: base.snapshot,
  });
  assert.equal(afterPageReturned.status, 'rejected');
});

test('invalid or excluded sampling commitments do not contribute', () => {
  for (let index = 0; index < 32; index += 1) {
    const id = `sampling-roundtrip-${index}`;
    const commitment = createSamplingCommitment(id, 0.5);
    assert.equal(verifySamplingCommitment(id, commitment), true);
  }

  const excludedCommitment = createSamplingCommitment('unsampled', 0);
  const excluded = ingestEvidence(null, [
    policyEvent(),
    failure('unsampled', { sampling: excludedCommitment }),
  ]);
  assert.equal(excluded.candidates.length, 0);
  assert.equal(excluded.denied[0]?.reason, 'sampling_excluded');

  const valid = createSamplingCommitment('bad-promise', 1);
  const invalid = ingestEvidence(null, [
    policyEvent(),
    failure('bad-promise', { sampling: { ...valid, promise: 'forged' } }),
  ]);
  assert.equal(invalid.candidates.length, 0);
  assert.equal(invalid.denied[0]?.reason, 'invalid_sampling_commitment');
});

test('snapshots are frozen and integrity-checked', () => {
  const { snapshot } = ingestEvidence(null, [policyEvent(), failure('freeze-1')]);
  assert.equal(verifySnapshotIntegrity(snapshot), true);
  assert.equal(Object.isFrozen(snapshot), true);
  assert.equal(Object.isFrozen(snapshot.events[0]), true);
  assert.throws(() => {
    (snapshot.events as unknown as EvidenceEvent[]).push(policyEvent({ policyId: 'mutated' }));
  });
});

test('ReportingHost incrementally verifies, delivers, acknowledges, and blocks', () => {
  const host = new ReportingHost({ defaultMaxContributions: 1 });
  host.ingest(policyEvent({ batchMaxContributions: 1 }));
  const ingested = host.ingest(failure('host-1'));
  assert.equal(ingested.candidates[0]?.state, 'sealed');
  const candidate = host.candidates[0];

  const verification = host.verify(candidate);
  assert.equal(verification.status, 'verified');
  const approval = host.approve(candidate);
  assert.equal(approval.ok, true);
  if (!approval.ok) {
    return;
  }
  const deliveryId = approval.delivery.deliveryId;
  assert.equal(host.beginDelivery(deliveryId).ok, true);
  assert.equal(host.acknowledgeDelivery(deliveryId)?.status, 'acknowledged');

  host.ingest({
    id: 'host-revoke',
    kind: 'report-permission-revoked',
    occurredAt: 30,
    tenantId: 'tenant-a',
    principalId: 'principal-a',
  });
  assert.equal(host.beginDelivery(deliveryId).ok, false);
});
