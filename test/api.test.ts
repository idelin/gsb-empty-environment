import assert from "node:assert/strict";
import test from "node:test";
import api, {
  KeyAgent,
  buildMigrationScenario,
  compareTcb,
  createHmacVerifier,
  evaluateRelease,
  signRecordHmac,
} from "../index.ts";

test("package entry points and positional forms remain interchangeable", () => {
  const scenario = buildMigrationScenario();
  assert.equal(typeof api.evaluateRelease, "function");

  const object = evaluateRelease(scenario.input);
  const positional = evaluateRelease(
    scenario.options.workloadId,
    scenario.options.generation,
    scenario.events,
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  const arrayForm = evaluateRelease(scenario.events, {
    workloadId: scenario.options.workloadId,
    generation: scenario.options.generation,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  const generationFirst = evaluateRelease(
    scenario.options.generation,
    scenario.events,
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  for (const decision of [positional, arrayForm, generationFirst]) {
    assert.equal(decision.status, object.status);
    assert.equal(decision.targetKey, object.targetKey);
    assert.deepEqual(decision.dependencies, object.dependencies);
  }

  const engine = new KeyAgent({
    policy: scenario.policy,
    events: scenario.events,
    closedDevices: scenario.input.closedDevices,
  });
  const engineDecision = engine.decide(
    scenario.options.workloadId,
    scenario.options.generation,
  );
  assert.equal(engineDecision.status, "release");
});

test("workload and generation can be inferred from the event set", () => {
  const scenario = buildMigrationScenario();
  const inferred = evaluateRelease({
    events: scenario.events,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  assert.equal(inferred.status, "release");
  assert.equal(inferred.workloadId, scenario.options.workloadId);
  assert.equal(inferred.generation, scenario.options.generation);

  const mixedGenerations = scenario.events.map((event) =>
    event.id === scenario.oldAttestationId &&
    event.payload &&
    typeof event.payload === "object"
      ? {
          ...event,
          payload: {
            ...event.payload,
            generation: scenario.options.generation - 1,
          },
        }
      : event,
  );
  const mixed = evaluateRelease({
    events: mixedGenerations,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  assert.equal(mixed.status, "release");
  assert.equal(mixed.generation, scenario.options.generation);
});

test("grouped input buckets and closed-device maps are accepted", () => {
  const scenario = buildMigrationScenario();
  const byAction = (action: string) =>
    scenario.events.filter((event) => event.action === action);
  const decision = evaluateRelease({
    workloadId: scenario.options.workloadId,
    generation: scenario.options.generation,
    events: {
      attestations: byAction("attest"),
      deactivations: byAction("deactivate-instance"),
      revocations: byAction("revoke-key"),
      transfers: byAction("accept-transfer"),
      confirmations: byAction("confirm-migration"),
    },
    policy: scenario.policy,
    closedDevices: { [scenario.options.oldChipId]: "closed" },
  });
  assert.equal(decision.status, "release");
});

test("records without migration ids still form one transaction by strong bindings", () => {
  const scenario = buildMigrationScenario();
  const unbound = scenario.events.map((event) => ({
    ...event,
    payload:
      event.payload && typeof event.payload === "object"
        ? { ...event.payload, migrationId: undefined }
        : event.payload,
  }));
  const decision = evaluateRelease({
    ...scenario.input,
    events: unbound,
  });
  assert.equal(decision.status, "release");
  assert.equal(decision.targetKey, scenario.options.newRuntimeKey);
  assert.equal(decision.migrations.length, 1);
});

test("HMAC signatures are supported and still bind actions", () => {
  const scenario = buildMigrationScenario();
  const secret = "test-secret";
  const signers = new Set(scenario.events.map((event) => String(event.signer)));
  const verifier = createHmacVerifier(
    Object.fromEntries([...signers].map((signer) => [signer, secret])),
  );
  const signed = scenario.events.map((event) => signRecordHmac(event, secret));
  const valid = evaluateRelease({
    ...scenario.input,
    events: signed,
    verifier,
  });
  assert.equal(valid.status, "release");

  const tampered = signed.map((event) =>
    event.id === scenario.deactivationId
      ? { ...event, action: "accept-transfer" }
      : event,
  );
  const invalid = evaluateRelease({
    ...scenario.input,
    events: tampered,
    verifier,
  });
  assert.equal(invalid.status, "reject");
});

test("string and component-array TCB forms normalize", () => {
  assert.equal(compareTcb("svn=7,fmw=11", "svn=6,fmw=10"), "newer");
  assert.equal(
    compareTcb(
      [
        { component: "svn", version: 7 },
        { component: "fmw", version: 11 },
      ],
      { svn: 6, fmw: 10 },
    ),
    "newer",
  );
});
