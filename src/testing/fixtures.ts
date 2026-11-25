import { canonicalizeForm, defineAliases } from "../canonical.ts";
import { signGrant, type SigningKey } from "../sign.ts";
import type { SignedGrant, SubmitRequest } from "../types.ts";

export const testKey: SigningKey = { kid: "test-key-1", secret: "unit-test-secret" };

export const testAliases = defineAliases([
  ["beneficiary", ["payee", "legalName"]],
  ["amt", ["amount"]],
]);

export interface FixtureOptions {
  readonly grantId?: string;
  readonly subject?: string;
  readonly windowSessionId?: string;
  readonly objectVersion?: string;
  readonly form?: Readonly<Record<string, unknown>>;
  readonly maxActionConsumptions?: number;
  readonly maxSubConsumptions?: number;
  readonly actionId?: string;
  readonly expiresAt?: number;
  /** Two parent actions reuse subId "step" to prove parent-scoped uniqueness. */
  readonly twoActions?: boolean;
}

export function buildGrant(options: FixtureOptions = {}): SignedGrant {
  const form = options.form ?? { beneficiary: "Acme GmbH", amt: 4200, memo: "invoice-7" };
  const { digest } = canonicalizeForm(form, testAliases);
  const baseSubs = [
    { subId: "reserve", meaning: "reserve balance", dependsOn: [] as readonly string[], maxSubConsumptions: options.maxSubConsumptions ?? 1 },
    { subId: "ledger", meaning: "post ledger entry", dependsOn: ["reserve"] as readonly string[], maxSubConsumptions: options.maxSubConsumptions ?? 1 },
    { subId: "notify", meaning: "notify payee", dependsOn: ["ledger"] as readonly string[], maxSubConsumptions: options.maxSubConsumptions ?? 1 },
  ];
  const actions = options.twoActions
    ? [
        {
          actionId: "confirm",
          meaning: "confirm payment",
          maxActionConsumptions: options.maxActionConsumptions ?? 2,
          subOperations: baseSubs,
        },
        {
          actionId: "confirm-alt",
          meaning: "alternate payment confirmation",
          maxActionConsumptions: 1,
          subOperations: [
            { subId: "reserve", meaning: "alt reserve", dependsOn: [] as readonly string[], maxSubConsumptions: 1 },
            { subId: "step", meaning: "alt step", dependsOn: ["reserve"] as readonly string[], maxSubConsumptions: 1 },
          ],
        },
      ]
    : [
        {
          actionId: options.actionId ?? "confirm",
          meaning: "confirm payment",
          maxActionConsumptions: options.maxActionConsumptions ?? 2,
          subOperations: baseSubs,
        },
      ];
  return signGrant(
    {
      grantId: options.grantId ?? "grant-1",
      subject: options.subject ?? "user-42",
      windowSessionId: options.windowSessionId ?? "window-A",
      objectVersion: options.objectVersion ?? "obj-v7",
      formDigest: digest,
      actions,
      expiresAt: options.expiresAt ?? Date.now() + 60_000,
      kid: testKey.kid,
    },
    testKey,
  );
}

export function buildSubmit(
  grant: SignedGrant,
  overrides: Partial<SubmitRequest> = {},
): SubmitRequest {
  const action = grant.actions[0];
  // Deliberately shuffled: the service must re-order by dependencies.
  const shuffled = [...action.subOperations].reverse().map((spec) => ({
    subId: spec.subId,
    payload: { valueFor: spec.subId },
  }));
  return {
    grant,
    subject: grant.subject,
    windowSessionId: grant.windowSessionId,
    objectVersion: grant.objectVersion,
    form: { beneficiary: "Acme GmbH", amt: 4200, memo: "invoice-7" },
    actionId: action.actionId,
    idemKey: "idem-1",
    subOperations: shuffled,
    ...overrides,
  };
}
