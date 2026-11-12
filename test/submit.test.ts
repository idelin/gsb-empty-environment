import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { SubmitError } from "../src/types.ts";
import type {
  FormSnapshot,
  NavigationState,
  PreparedRequest,
  TransportResult,
} from "../src/types.ts";
import { createSubmitPlan } from "../src/plan.ts";
import { submitPlan, verifyPlan } from "../src/submit.ts";
import { OriginAllowlistPolicy } from "../src/dom.ts";

const ORIGIN = "https://app.example.test";

function snapshot(overrides: Partial<FormSnapshot> = {}): FormSnapshot {
  return {
    documentGeneration: 1,
    origin: ORIGIN,
    baseUrl: `${ORIGIN}/checkout/`,
    action: "charge",
    method: "post",
    enctype: "application/x-www-form-urlencoded",
    entries: [{ name: "amount", value: "100" }],
    ...overrides,
  };
}

interface FakeDocument extends NavigationState {
  navigate(nextGeneration: number, nextOrigin: string, nextBase: string): void;
  swapBase(nextBase: string): void;
}

function fakeDocument(initial: Partial<NavigationState> = {}): FakeDocument {
  const state: NavigationState = {
    generation: initial.generation ?? 1,
    origin: initial.origin ?? ORIGIN,
    baseUrl: initial.baseUrl ?? `${ORIGIN}/checkout/`,
  };
  return {
    get generation() {
      return state.generation;
    },
    get origin() {
      return state.origin;
    },
    get baseUrl() {
      return state.baseUrl;
    },
    navigate(nextGeneration, nextOrigin, nextBase) {
      state.generation = nextGeneration;
      state.origin = nextOrigin;
      state.baseUrl = nextBase;
    },
    swapBase(nextBase) {
      state.baseUrl = nextBase;
    },
  };
}

class RecordingTransport {
  readonly sent: PreparedRequest[] = [];
  private readonly contactUrl?: string;

  constructor(contactUrl?: string) {
    this.contactUrl = contactUrl;
  }

  async dispatch(request: PreparedRequest): Promise<TransportResult> {
    this.sent.push(request);
    return {
      ok: true,
      status: 200,
      url: this.contactUrl ?? request.url,
    };
  }
}

function onlySent(transport: RecordingTransport): PreparedRequest {
  assert.equal(transport.sent.length, 1);
  return transport.sent[0] as PreparedRequest;
}

describe("submission-time verification", () => {
  it("dispatches to the approved target when the document is unchanged", async () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());
    const transport = new RecordingTransport();

    const outcome = await submitPlan(
      plan,
      () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
      transport,
    );

    assert.equal(transport.sent.length, 1);
    assert.equal(onlySent(transport).url, "https://app.example.test/checkout/charge");
    assert.equal(outcome.result.url, "https://app.example.test/checkout/charge");
  });

  it("blocks submission after a navigation bumps the document generation", async () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());
    const transport = new RecordingTransport();

    doc.navigate(2, ORIGIN, `${ORIGIN}/checkout/`);

    await assert.rejects(
      () =>
        submitPlan(
          plan,
          () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
          transport,
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "STALE_GENERATION",
    );
    assert.equal(transport.sent.length, 0);
  });

  it("blocks when the document origin changed even if the generation was spoofed equal", () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());

    doc.navigate(1, "https://evil.example.test", "https://evil.example.test/checkout/");

    assert.throws(
      () =>
        verifyPlan(plan, {
          generation: doc.generation,
          origin: doc.origin,
          baseUrl: doc.baseUrl,
        }),
      (error: unknown) => error instanceof SubmitError && error.code === "ORIGIN_MISMATCH",
    );
  });

  it("allows a base-only swap without navigation but still hits the frozen target", async () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());
    doc.swapBase("https://evil.example.test/checkout/");

    const transport = new RecordingTransport();
    const outcome = await submitPlan(
      plan,
      () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
      transport,
    );

    assert.equal(doc.generation, 1);
    assert.equal(onlySent(transport).url, "https://app.example.test/checkout/charge");
    assert.equal(outcome.result.url, "https://app.example.test/checkout/charge");
  });

  it("re-checks the target policy at submission time and blocks revoked permission", async () => {
    const doc = fakeDocument();
    const policy = new OriginAllowlistPolicy([
      ORIGIN,
      "https://payments.example.test",
    ]);
    const plan = createSubmitPlan(
      snapshot({ action: "https://payments.example.test/v1/charge" }),
      null,
      { policy },
    );
    const transport = new RecordingTransport();

    const revokedPolicy = new OriginAllowlistPolicy([ORIGIN]);
    await assert.rejects(
      () =>
        submitPlan(
          plan,
          () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
          transport,
          { policy: revokedPolicy },
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "PERMISSION_REVOKED",
    );
    assert.equal(transport.sent.length, 0);
  });
});

describe("navigation races against dispatch", () => {
  it("detects a transport that was redirected to another origin during the request", async () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());
    const transport = new RecordingTransport("https://evil.example.test/collect");

    await assert.rejects(
      () =>
        submitPlan(
          plan,
          () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
          transport,
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "TARGET_DRIFT",
    );
  });

  it("detects a navigation that happens while entry reading is in flight", async () => {
    const doc = fakeDocument();
    const plan = createSubmitPlan(snapshot());
    const transport = new RecordingTransport();

    const stateReader = () => {
      doc.navigate(2, ORIGIN, `${ORIGIN}/next`);
      return { generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl };
    };

    await assert.rejects(
      () => submitPlan(plan, stateReader, transport),
      (error: unknown) => error instanceof SubmitError && error.code === "STALE_GENERATION",
    );
    assert.equal(transport.sent.length, 0);
  });

  it("allows two sequential plans but never the stale one after navigation", async () => {
    const doc = fakeDocument();
    const firstPlan = createSubmitPlan(snapshot());
    doc.navigate(2, ORIGIN, `${ORIGIN}/checkout/`);
    const secondPlan = createSubmitPlan(snapshot({ documentGeneration: 2 }));
    const transport = new RecordingTransport();

    await assert.rejects(
      () =>
        submitPlan(
          firstPlan,
          () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
          transport,
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "STALE_GENERATION",
    );

    const outcome = await submitPlan(
      secondPlan,
      () => ({ generation: doc.generation, origin: doc.origin, baseUrl: doc.baseUrl }),
      transport,
    );
    assert.equal(transport.sent.length, 1);
    assert.equal(outcome.result.url, "https://app.example.test/checkout/charge");
  });
});

describe("button override race", () => {
  it("a retargeting button must be approved separately and cannot ride an existing plan", async () => {
    const doc = fakeDocument();
    const plainPlan = createSubmitPlan(snapshot());
    const transport = new RecordingTransport();

    doc.swapBase("https://evil.example.test/checkout/");
    assert.throws(
      () =>
        createSubmitPlan(
          snapshot({ baseUrl: doc.baseUrl }),
          { name: "express", formAction: "express" },
        ),
      (error: unknown) => error instanceof SubmitError && error.code === "TARGET_DENIED",
    );
    assert.equal(transport.sent.length, 0);

    const outcome = await submitPlan(
      plainPlan,
      () => ({ generation: 1, origin: ORIGIN, baseUrl: doc.baseUrl }),
      transport,
    );
    assert.equal(outcome.result.url, "https://app.example.test/checkout/charge");
  });
});
