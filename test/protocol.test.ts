import assert from "node:assert/strict";
import { appendFile, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  CrossDeviceAuthHost,
  FaultInjectingJournalStore,
  FileJournalStore,
  InMemoryIssuanceService,
  MemoryJournalStore,
  createAuthHost,
  createBrowserCredentialDouble,
  createBrowserProof,
  createDeviceConfirmation,
  createDeviceCredentialDouble,
  createEd25519HostKeyPair,
  deriveTransactionId,
  verifyEmailDeliveryPair,
  verifyIssuedAuthorization,
  verifyReceipt,
  verifyReceiptChain,
} from "../src/index.ts";
import type {
  AuthEvent,
  ChallengeStatus,
  EmailToken,
  HostKeyPair,
  IssuedCredential,
} from "../src/index.ts";

interface TestContext {
  host: CrossDeviceAuthHost;
  store: MemoryJournalStore;
  issuance: InMemoryIssuanceService;
  hostKeyPair: HostKeyPair;
  clock: () => number;
  setNow: (value: number) => void;
}

async function makeHost(
  options: {
    now?: number;
    store?: MemoryJournalStore;
    hostKeyPair?: HostKeyPair;
    issuance?: InMemoryIssuanceService;
  } = {},
): Promise<TestContext> {
  let now = options.now ?? 1_700_000_000_000;
  const clock = () => now;
  const store = options.store ?? new MemoryJournalStore();
  const hostKeyPair = options.hostKeyPair ?? createEd25519HostKeyPair();
  const issuance = options.issuance ?? new InMemoryIssuanceService({ clock });
  const host = await createAuthHost({ store, issuance, clock, hostKeyPair });
  return {
    host,
    store,
    issuance,
    hostKeyPair,
    clock,
    setNow: (value: number) => {
      now = value;
    },
  };
}

function assertState(status: ChallengeStatus, state: ChallengeStatus["state"]): void {
  assert.equal(status.state, state);
  assert.equal(status.status, state);
}

test("scanner prefetch does not consume authorization; delivery receipts and issuance verify offline", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });

  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    browserSessionId: "window-1",
  });
  assertState(login.status, "awaiting_device_confirmation");
  assert.equal(login.emailToken, login.token.encoded);

  const scanner = await context.host.recordEmailOpened({
    token: login.emailToken,
    kind: "scanner",
    accessId: "scanner-hit-1",
  });
  assert.equal(scanner.recorded, true);
  assert.equal(scanner.consumedAuthorization, false);
  assert.equal(scanner.status.scannerAccessCount, 1);
  assertState(scanner.status, "awaiting_device_confirmation");

  const scannerAgain = await context.host.recordEmailOpened({
    token: login.emailToken,
    kind: "scanner",
    accessId: "scanner-hit-1",
  });
  assert.equal(scannerAgain.duplicate, true);
  assert.equal(scannerAgain.status.emailOpenCount, 1);

  const delivery = await context.host.recordEmailDelivered({
    token: login.emailToken,
    deliveryId: "delivery-1",
    providerRef: "smtp-123",
  });
  assert.equal(delivery.delivered, true);
  assert.ok(delivery.receipt);
  const deliveryCheck = verifyEmailDeliveryPair(
    login.dispatchReceipt,
    delivery.receipt!,
    context.host.publicKey,
  );
  assert.equal(deliveryCheck.accepted, true);
  assert.equal(deliveryCheck.tokenId, login.tokenId);

  // A scanner hit after delivery also does not consume anything.
  const human = await context.host.recordEmailOpened({
    token: login.token,
    kind: "human",
    accessId: "human-open-1",
  });
  assert.equal(human.status.emailOpenCount, 2);
  assertState(human.status, "awaiting_device_confirmation");

  const confirmation = createDeviceConfirmation(device, login.emailToken, {
    deviceId: "device-a",
  });
  const confirmed = await context.host.confirmOnDevice(confirmation);
  assert.equal(confirmed.accepted, true);
  assertState(confirmed.status, "awaiting_browser_proof");

  const proof = createBrowserProof(browser, login.emailToken);
  const redeemed = await context.host.redeem({ token: login.emailToken, proof });
  assertState(redeemed, "issued");
  assert.equal(redeemed.redeemed, true);
  assert.equal(redeemed.newlyIssued, true);
  assert.ok(redeemed.receipt);
  assert.equal(context.issuance.issueCalls, 1);

  const offline = verifyIssuedAuthorization(redeemed.receipt!, context.host.publicKey);
  assert.equal(offline.accepted, true, offline.reason);
  assert.equal(offline.accountId, "account-a");
  assert.equal(offline.browserId, "browser-a");
  assert.equal(offline.browserSessionId, "window-1");
  assert.equal(offline.transactionId, deriveTransactionId(login.token.claims));

  // Receipts are immutable, self-contained and signed.
  assert.equal(Object.isFrozen(redeemed.receipt), true);
  assert.equal(Object.isFrozen(redeemed.receipt!.payload), true);
  assert.equal(verifyReceipt(redeemed.receipt!, context.host.publicKey), true);
  assert.equal(verifyReceiptChain(context.host.receipts(), context.host.publicKey), true);

  const repeated = await context.host.redeem({ proof });
  assertState(repeated, "issued");
  assert.equal(repeated.receipt!.hash, redeemed.receipt!.hash);
  assert.equal(context.issuance.issueCalls, 1);

  // The email link/status endpoint is visible to scanners: it reports `issued`
  // but never exposes the credential or the receipt that embeds it.
  const statusOnly = await context.host.getStatus(login.emailToken);
  assertState(statusOnly, "issued");
  assert.equal(statusOnly.credential, undefined);
  assert.equal(statusOnly.receipt, undefined);
  assert.equal(statusOnly.receiptHash, redeemed.receipt!.hash);

  // Even after issuance, a party without the bound browser key cannot claim it.
  const unboundClaim = await context.host.redeem({
    proof: createBrowserProof(createBrowserCredentialDouble(), login.emailToken),
  });
  assertState(unboundClaim, "awaiting_browser_proof");
  assert.equal(unboundClaim.redeemed, false);
  assert.equal(unboundClaim.credential, undefined);
  assert.equal(unboundClaim.receipt, undefined);
});

test("two windows with the same user code stay bound to their own token and browser", async () => {
  const context = await makeHost();
  const browserA = createBrowserCredentialDouble();
  const browserB = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browserA.publicKey,
  });
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-b",
    publicKey: browserB.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });

  const windowA = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    browserSessionId: "window-a",
  });
  const windowB = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-b",
    browserSessionId: "window-b",
  });
  assert.equal(windowA.userCode, windowB.userCode);
  assert.notEqual(windowA.tokenId, windowB.tokenId);

  const wrongSelection = createDeviceConfirmation(device, windowA.emailToken, {
    deviceId: "device-a",
    browserId: "browser-b",
    browserSessionId: "window-b",
  });
  const rejectedSelection = await context.host.confirmOnDevice(wrongSelection);
  assert.equal(rejectedSelection.accepted, false);
  assert.equal(rejectedSelection.reason, "selected_browser_mismatch");
  assertState(rejectedSelection.status, "awaiting_device_confirmation");

  const confirmedA = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, windowA.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(confirmedA.accepted, true);
  assertState(confirmedA.status, "awaiting_browser_proof");
  assertState(
    await context.host.getStatus(windowB.emailToken),
    "awaiting_device_confirmation",
  );

  const crossWindowProof = createBrowserProof(browserB, windowB.emailToken);
  const notYetConfirmed = await context.host.redeem({ proof: crossWindowProof });
  assertState(notYetConfirmed, "awaiting_device_confirmation");
  assert.equal(context.issuance.issueCalls, 0);

  const wrongKey = createBrowserProof(browserB, windowA.emailToken);
  const wrongKeyResult = await context.host.redeem({ proof: wrongKey });
  assertState(wrongKeyResult, "awaiting_browser_proof");
  assert.equal(wrongKeyResult.reason, "invalid_browser_proof");
  assert.equal(context.issuance.issueCalls, 0);

  const issuedA = await context.host.redeem({
    proof: createBrowserProof(browserA, windowA.emailToken),
  });
  assertState(issuedA, "issued");

  const confirmedB = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, windowB.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(confirmedB.accepted, true);
  const issuedB = await context.host.redeem({
    proof: createBrowserProof(browserB, windowB.emailToken),
  });
  assertState(issuedB, "issued");
  assert.equal(context.issuance.issueCalls, 2);
  assert.notEqual(issuedA.transactionId, issuedB.transactionId);
});

test("delivery receipts link by dispatch hash even when multiple emails interleave", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  const first = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  const second = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  const firstDelivery = await context.host.recordEmailDelivered({
    token: first.emailToken,
    deliveryId: "delivery-first",
  });
  const secondDelivery = await context.host.recordEmailDelivered({
    token: second.emailToken,
    deliveryId: "delivery-second",
  });
  assert.equal(
    verifyEmailDeliveryPair(first.dispatchReceipt, firstDelivery.receipt!, context.host.publicKey)
      .accepted,
    true,
  );
  assert.equal(
    verifyEmailDeliveryPair(second.dispatchReceipt, secondDelivery.receipt!, context.host.publicKey)
      .accepted,
    true,
  );

  const duplicate = await context.host.recordEmailDelivered({
    token: first.emailToken,
    deliveryId: "delivery-first",
  });
  assert.equal(duplicate.delivered, true);
  assert.equal(duplicate.duplicate, true);
  const conflict = await context.host.recordEmailDelivered({
    token: first.emailToken,
    deliveryId: "delivery-other",
  });
  assert.equal(conflict.delivered, false);
  assert.equal(conflict.reason, "delivery_conflict");
});

test("the same email address under another account cannot approve or receive an authorization", async () => {
  const context = await makeHost();
  const browserA = createBrowserCredentialDouble();
  const browserB = createBrowserCredentialDouble();
  const deviceA = createDeviceCredentialDouble();
  const deviceB = createDeviceCredentialDouble();
  for (const [accountId, browserId, browser] of [
    ["account-a", "browser-a", browserA],
    ["account-b", "browser-b", browserB],
  ] as const) {
    await context.host.registerBrowser({ accountId, browserId, publicKey: browser.publicKey });
  }
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: deviceA.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-b",
    deviceId: "device-b",
    publicKey: deviceB.publicKey,
  });

  const loginA = await context.host.beginLogin({
    accountId: "account-a",
    email: "shared@example.test",
    browserId: "browser-a",
    userCode: "SAME-CODE",
  });
  const loginB = await context.host.beginLogin({
    accountId: "account-b",
    email: "shared@example.test",
    browserId: "browser-b",
    userCode: "SAME-CODE",
  });

  const crossAccount = await context.host.confirmOnDevice(
    createDeviceConfirmation(deviceB, loginA.emailToken, { deviceId: "device-b" }),
  );
  assert.equal(crossAccount.accepted, false);
  assert.equal(crossAccount.reason, "device_account_mismatch");
  assertState(await context.host.getStatus(loginA.emailToken), "awaiting_device_confirmation");

  const confirmedA = await context.host.confirmOnDevice(
    createDeviceConfirmation(deviceA, loginA.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(confirmedA.accepted, true);
  assertState(await context.host.getStatus(loginB.emailToken), "awaiting_device_confirmation");

  const issuedA = await context.host.redeem({
    proof: createBrowserProof(browserA, loginA.emailToken),
  });
  assertState(issuedA, "issued");
  assert.equal(issuedA.accountId, "account-a");

  const confirmedB = await context.host.confirmOnDevice(
    createDeviceConfirmation(deviceB, loginB.emailToken, { deviceId: "device-b" }),
  );
  assert.equal(confirmedB.accepted, true);
  const issuedB = await context.host.redeem({
    proof: createBrowserProof(browserB, loginB.emailToken),
  });
  assertState(issuedB, "issued");
  assert.equal(issuedB.accountId, "account-b");
  assert.equal(context.issuance.issueCalls, 2);
});

test("browser and device ids are account-scoped, so equal ids under two accounts do not collide", async () => {
  const context = await makeHost();
  const browserA = createBrowserCredentialDouble();
  const browserB = createBrowserCredentialDouble();
  const deviceA = createDeviceCredentialDouble();
  const deviceB = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "shared-browser",
    publicKey: browserA.publicKey,
  });
  await context.host.registerBrowser({
    accountId: "account-b",
    browserId: "shared-browser",
    publicKey: browserB.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "shared-device",
    publicKey: deviceA.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-b",
    deviceId: "shared-device",
    publicKey: deviceB.publicKey,
  });

  const loginA = await context.host.beginLogin({
    accountId: "account-a",
    email: "shared@example.test",
    browserId: "shared-browser",
  });
  const loginB = await context.host.beginLogin({
    accountId: "account-b",
    email: "shared@example.test",
    browserId: "shared-browser",
  });

  const wrongApprover = await context.host.confirmOnDevice(
    createDeviceConfirmation(deviceB, loginA.emailToken, { deviceId: "shared-device" }),
  );
  assert.equal(wrongApprover.accepted, false);
  assert.equal(wrongApprover.reason, "device_key_mismatch");
  assertState(await context.host.getStatus(loginA.emailToken), "awaiting_device_confirmation");

  assert.equal(
    (
      await context.host.confirmOnDevice(
        createDeviceConfirmation(deviceA, loginA.emailToken, { deviceId: "shared-device" }),
      )
    ).accepted,
    true,
  );
  assert.equal(
    (
      await context.host.confirmOnDevice(
        createDeviceConfirmation(deviceB, loginB.emailToken, { deviceId: "shared-device" }),
      )
    ).accepted,
    true,
  );
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browserA, loginA.emailToken) }),
    "issued",
  );
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browserB, loginB.emailToken) }),
    "issued",
  );
});

test("out-of-order confirmations use token ids and never attach an old email to the newest one", async () => {
  const context = await makeHost({ now: 1_000_000 });
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });

  const first = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    ttlMs: 60_000,
  });
  context.setNow(1_000_010);
  const second = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    ttlMs: 60_000,
  });

  // Confirmation for the second message arrives first.
  const confirmSecond = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, second.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(confirmSecond.accepted, true);
  const confirmFirst = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, first.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(confirmFirst.accepted, true);

  assertState(await context.host.getStatus(first.emailToken), "awaiting_browser_proof");
  assertState(await context.host.getStatus(second.emailToken), "awaiting_browser_proof");
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browser, first.emailToken) }),
    "issued",
  );
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browser, second.emailToken) }),
    "issued",
  );

  // A confirmation directed at an expired challenge is reported as expired, and
  // the newer challenge remains untouched.
  context.setNow(2_000_000);
  const expiring = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    ttlMs: 1_000,
  });
  context.setNow(2_000_500);
  const newest = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
    ttlMs: 60_000,
  });
  context.setNow(2_001_500);
  const expiredConfirmation = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, expiring.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(expiredConfirmation.accepted, false);
  assert.equal(expiredConfirmation.reason, "challenge_expired");
  assertState(expiredConfirmation.status, "expired");
  assertState(await context.host.getStatus(expiring.emailToken), "expired");
  assertState(await context.host.getStatus(newest.emailToken), "awaiting_device_confirmation");
  const newestConfirmation = await context.host.confirmOnDevice(
    createDeviceConfirmation(device, newest.emailToken, { deviceId: "device-a" }),
  );
  assert.equal(newestConfirmation.accepted, true);
  assert.equal(context.issuance.issueCalls, 2);
});

test("browser key rotation retires old challenges and a fresh key starts a fresh challenge", async () => {
  const context = await makeHost();
  const oldBrowserKey = createBrowserCredentialDouble();
  const newBrowserKey = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: oldBrowserKey.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });

  const oldLogin = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, oldLogin.emailToken, { deviceId: "device-a" }),
  );
  await context.host.rotateBrowserKey({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: newBrowserKey.publicKey,
  });

  const withNewKey = await context.host.redeem({
    proof: createBrowserProof(newBrowserKey, oldLogin.emailToken),
  });
  assertState(withNewKey, "awaiting_browser_proof");
  assert.equal(withNewKey.reason, "invalid_browser_proof");

  const withOldKey = await context.host.redeem({
    proof: createBrowserProof(oldBrowserKey, oldLogin.emailToken),
  });
  assertState(withOldKey, "awaiting_browser_proof");
  assert.equal(withOldKey.reason, "browser_key_retired");
  assert.equal(context.issuance.issueCalls, 0);

  const fresh = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  assert.notEqual(fresh.browserKeyId, oldLogin.browserKeyId);
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, fresh.emailToken, { deviceId: "device-a" }),
  );
  const issued = await context.host.redeem({
    proof: createBrowserProof(newBrowserKey, fresh.emailToken),
  });
  assertState(issued, "issued");
  assert.equal(context.issuance.issueCalls, 1);
});

test("replacing or switching the registered device changes which private key can confirm", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const oldDevice = createDeviceCredentialDouble();
  const newDevice = createDeviceCredentialDouble();
  const extraDevice = createDeviceCredentialDouble();
  const thirdDevice = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "old-device",
    publicKey: oldDevice.publicKey,
  });

  const pending = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.replaceDevice({
    accountId: "account-a",
    oldDeviceId: "old-device",
    newDeviceId: "new-device",
    publicKey: newDevice.publicKey,
  });

  const oldConfirmation = await context.host.confirmOnDevice(
    createDeviceConfirmation(oldDevice, pending.emailToken, { deviceId: "old-device" }),
  );
  assert.equal(oldConfirmation.accepted, false);
  assert.equal(oldConfirmation.reason, "device_revoked");

  const newConfirmation = await context.host.confirmOnDevice(
    createDeviceConfirmation(newDevice, pending.emailToken, { deviceId: "new-device" }),
  );
  assert.equal(newConfirmation.accepted, true);
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browser, pending.emailToken) }),
    "issued",
  );

  // Switching to another registered device is also supported.
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "extra-device",
    publicKey: extraDevice.publicKey,
  });
  const second = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  const switched = await context.host.confirmOnDevice(
    createDeviceConfirmation(extraDevice, second.emailToken, {
      deviceId: "extra-device",
    }),
  );
  assert.equal(switched.accepted, true);
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browser, second.emailToken) }),
    "issued",
  );

  // Replacing a device invalidates a still-pending approval from that device.
  const third = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(newDevice, third.emailToken, { deviceId: "new-device" }),
  );
  await context.host.replaceDevice({
    accountId: "account-a",
    oldDeviceId: "new-device",
    newDeviceId: "third-device",
    publicKey: thirdDevice.publicKey,
  });
  const invalidated = await context.host.getStatus(third.emailToken);
  assertState(invalidated, "awaiting_device_confirmation");
  assert.equal(invalidated.reason, "device_confirmation_invalidated");
  assert.equal(invalidated.deviceConfirmed, false);
  const blocked = await context.host.redeem({
    proof: createBrowserProof(browser, third.emailToken),
  });
  assertState(blocked, "awaiting_device_confirmation");
  const replacementConfirmation = await context.host.confirmOnDevice(
    createDeviceConfirmation(thirdDevice, third.emailToken, {
      deviceId: "third-device",
    }),
  );
  assert.equal(replacementConfirmation.accepted, true);
  assertState(
    await context.host.redeem({ proof: createBrowserProof(browser, third.emailToken) }),
    "issued",
  );
});

test("revoking a browser before issuance intent prevents any signing", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );
  const revocation = await context.host.revokeBrowser({
    accountId: "account-a",
    browserId: "browser-a",
  });
  assert.equal(revocation.revoked, true);
  assert.ok(revocation.receipt);

  const result = await context.host.redeem({
    proof: createBrowserProof(browser, login.emailToken),
  });
  assertState(result, "revoked");
  assert.equal(context.issuance.issueCalls, 0);
  assert.equal(context.host.authorizationAudit().authorizedPrincipals.length, 0);
  assert.equal(context.host.authorizationAudit().accounts[0]!.authorizations.length, 0);
});

test("revocation interleaved with an in-flight issuance cannot erase an already signed authorization", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );

  const release = context.issuance.blockNextIssue();
  const redeemPromise = context.host.redeem({
    proof: createBrowserProof(browser, login.emailToken),
  });
  await context.issuance.waitForIssueStarted();
  await context.host.revokeBrowser({ accountId: "account-a", browserId: "browser-a" });
  release();

  const result = await redeemPromise;
  assertState(result, "issued");
  assert.equal(result.browserRevoked, true);
  assert.equal(context.issuance.issueCalls, 1);
  const audit = context.host.authorizationAudit();
  assert.equal(audit.accounts[0]!.authorizations.length, 1);
  assert.equal(audit.accounts[0]!.authorizations[0]!.valid, false);
  assert.equal(audit.accounts[0]!.authorizations[0]!.browserRevoked, true);

  const repeat = await context.host.redeem({
    proof: createBrowserProof(browser, login.emailToken),
  });
  assertState(repeat, "issued");
  assert.equal(context.issuance.issueCalls, 1);
});

test("a lost issuance receipt becomes unknown, then resolves by lookup of the same transaction only", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );

  context.issuance.dropNextResponseAfterSuccess();
  const proof = createBrowserProof(browser, login.emailToken);
  const unknown = await context.host.redeem({ proof });
  assertState(unknown, "unknown");
  assert.equal(unknown.reason, "issuance_result_unknown");
  assert.equal(context.issuance.issueCalls, 1);
  assert.equal(context.issuance.lookupCalls, 0);
  assert.equal(context.host.authorizationAudit().accounts[0]!.authorizations.length, 0);

  const resolved = await context.host.redeem({ proof });
  assertState(resolved, "issued");
  assert.equal(resolved.newlyIssued, true);
  assert.equal(context.issuance.issueCalls, 1);
  assert.equal(context.issuance.lookupCalls, 1);
  assert.equal(resolved.transactionId, deriveTransactionId(login.token.claims));

  const again = await context.host.redeem({ proof });
  assertState(again, "issued");
  assert.equal(again.receipt!.hash, resolved.receipt!.hash);
  assert.equal(context.issuance.issueCalls, 1);
  assert.equal(context.issuance.lookupCalls, 1);
});

test("restarting after a lost receipt recovers by same-transaction lookup, not a second signature", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );
  const proof = createBrowserProof(browser, login.emailToken);
  context.issuance.dropNextResponseAfterSuccess();
  assertState(await context.host.redeem({ proof }), "unknown");

  const restarted = await createAuthHost({
    store: context.store,
    issuance: context.issuance,
    clock: context.clock,
    hostKeyPair: context.hostKeyPair,
  });
  const recovered = await restarted.redeem({ proof });
  assertState(recovered, "issued");
  assert.equal(recovered.redeemed, true);
  assert.equal(context.issuance.issueCalls, 1);
  assert.equal(context.issuance.lookupCalls, 1);
  assert.equal(
    verifyIssuedAuthorization(recovered.receipt!, context.host.publicKey).accepted,
    true,
  );
});

test("every durable prefix can be recovered and identifies the same authorized principals", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  await context.host.recordEmailOpened({
    token: login.emailToken,
    kind: "scanner",
    accessId: "scanner-1",
  });
  await context.host.recordEmailDelivered({
    token: login.emailToken,
    deliveryId: "delivery-1",
  });
  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );
  const proof = createBrowserProof(browser, login.emailToken);
  const final = await context.host.redeem({ proof });
  assertState(final, "issued");
  const finalCredential = final.credential!;
  const fullJournal = context.store.snapshot();
  assert.ok(fullJournal.length >= 8);

  const transactionId = deriveTransactionId(login.token.claims);
  for (let prefixLength = 0; prefixLength <= fullJournal.length; prefixLength += 1) {
    const prefix = fullJournal.slice(0, prefixLength);
    const eventTypes = prefix.map((record) => (record.event as AuthEvent).type);
    const issuance = new InMemoryIssuanceService({ clock: context.clock });
    issuance.transactions.set(transactionId, {
      status: "issued",
      credential: finalCredential as IssuedCredential,
    });
    const recovered = await CrossDeviceAuthHost.open({
      store: new MemoryJournalStore(prefix),
      issuance,
      clock: context.clock,
      hostKeyPair: context.hostKeyPair,
    });
    const audit = recovered.authorizationAudit();
    assert.ok(audit.generatedAt >= 0);
    const hasIssuedResult = eventTypes.includes("issuance.result");
    const hasIntent = eventTypes.includes("issuance.intent");
    const hasConfirmation = eventTypes.includes("device.confirmed");
    const hasLogin = eventTypes.includes("login.started");
    const authorizations = audit.accounts.flatMap((account) => account.authorizations);
    if (hasIssuedResult) {
      assert.equal(authorizations.length, 1, `issued prefix ${prefixLength}`);
      assert.equal(authorizations[0]!.valid, true);
      assert.equal(authorizations[0]!.transactionId, transactionId);
    } else {
      assert.equal(authorizations.length, 0, `unissued prefix ${prefixLength}`);
    }

    if (!hasLogin) {
      assertState(await recovered.getStatus(login.tokenId), "unknown");
    } else if (hasIssuedResult) {
      assertState(await recovered.getStatus(login.emailToken), "issued");
    } else if (hasIntent) {
      assertState(await recovered.getStatus(login.emailToken), "unknown");
      const resumed = await recovered.redeem({ proof });
      assertState(resumed, "issued");
      assert.equal(issuance.issueCalls, 0);
      assert.equal(issuance.lookupCalls, 1);
    } else if (hasConfirmation) {
      assertState(await recovered.getStatus(login.emailToken), "awaiting_browser_proof");
    } else {
      assertState(await recovered.getStatus(login.emailToken), "awaiting_device_confirmation");
    }
  }
});

test("file journal recovery discards a torn tail and keeps a complete prefix", async () => {
  const directory = await mkdtemp(join(tmpdir(), "cross-device-auth-"));
  const path = join(directory, "journal.log");
  try {
    const clock = () => 1_700_000_000_000;
    const hostKeyPair = createEd25519HostKeyPair();
    const issuance = new InMemoryIssuanceService({ clock });
    const browser = createBrowserCredentialDouble();
    const first = await createAuthHost({
      store: new FileJournalStore(path),
      issuance,
      clock,
      hostKeyPair,
    });
    await first.registerBrowser({
      accountId: "account-a",
      browserId: "browser-a",
      publicKey: browser.publicKey,
    });

    await appendFile(path, '{"v":1,"seq":1,"event":');
    const recovered = await createAuthHost({
      store: new FileJournalStore(path),
      issuance,
      clock,
      hostKeyPair,
    });
    const audit = recovered.authorizationAudit();
    assert.equal(audit.accounts.length, 1);
    assert.equal(audit.accounts[0]!.browsers.length, 1);

    const registration = await recovered.registerBrowser({
      accountId: "account-a",
      browserId: "browser-a",
      publicKey: browser.publicKey,
    });
    assert.equal(registration.idempotent, true);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("an interrupted append is either absent or fully present on restart", async () => {
  const clock = () => 1_700_000_000_000;
  const hostKeyPair = createEd25519HostKeyPair();
  const browser = createBrowserCredentialDouble();

  const beforeInner = new MemoryJournalStore();
  const beforeHost = await createAuthHost({
    store: new FaultInjectingJournalStore(beforeInner, 1, "before_append"),
    issuance: new InMemoryIssuanceService({ clock }),
    clock,
    hostKeyPair,
  });
  await assert.rejects(
    beforeHost.registerBrowser({
      accountId: "account-a",
      browserId: "browser-a",
      publicKey: browser.publicKey,
    }),
  );
  const beforeRestart = await createAuthHost({
    store: beforeInner,
    issuance: new InMemoryIssuanceService({ clock }),
    clock,
    hostKeyPair,
  });
  assert.equal(beforeRestart.authorizationAudit().accounts.length, 0);

  const afterInner = new MemoryJournalStore();
  const afterHost = await createAuthHost({
    store: new FaultInjectingJournalStore(afterInner, 1, "after_append"),
    issuance: new InMemoryIssuanceService({ clock }),
    clock,
    hostKeyPair,
  });
  await assert.rejects(
    afterHost.registerBrowser({
      accountId: "account-a",
      browserId: "browser-a",
      publicKey: browser.publicKey,
    }),
  );
  const afterRestart = await createAuthHost({
    store: afterInner,
    issuance: new InMemoryIssuanceService({ clock }),
    clock,
    hostKeyPair,
  });
  const audit = afterRestart.authorizationAudit();
  assert.equal(audit.accounts.length, 1);
  assert.equal(audit.accounts[0]!.browsers.length, 1);
  assert.equal(
    (
      await afterRestart.registerBrowser({
        accountId: "account-a",
        browserId: "browser-a",
        publicKey: browser.publicKey,
      })
    ).idempotent,
    true,
  );
});

test("issuance cannot be triggered by a browser proof before device confirmation", async () => {
  const context = await makeHost();
  const browser = createBrowserCredentialDouble();
  const device = createDeviceCredentialDouble();
  await context.host.registerBrowser({
    accountId: "account-a",
    browserId: "browser-a",
    publicKey: browser.publicKey,
  });
  await context.host.registerDevice({
    accountId: "account-a",
    deviceId: "device-a",
    publicKey: device.publicKey,
  });
  const login = await context.host.beginLogin({
    accountId: "account-a",
    email: "person@example.test",
    browserId: "browser-a",
  });
  const proof = createBrowserProof(browser, login.emailToken);
  const early = await context.host.redeem({ proof });
  assertState(early, "awaiting_device_confirmation");
  assert.equal(context.issuance.issueCalls, 0);

  await context.host.confirmOnDevice(
    createDeviceConfirmation(device, login.emailToken, { deviceId: "device-a" }),
  );
  const issued = await context.host.redeem({ proof });
  assertState(issued, "issued");
  assert.equal(context.issuance.issueCalls, 1);
});
