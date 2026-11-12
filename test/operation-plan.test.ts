import assert from "node:assert/strict";
import test from "node:test";
import {
  OperationPlanRejectedError,
  SimulatedBrowser,
  captureOperationPlan,
  executeOperationPlan,
  type PlanRejectionCode,
  rotationDegrees,
  scaling,
} from "../index.ts";

function assertPoint(
  actual: { x: number; y: number },
  expected: { x: number; y: number },
  epsilon = 1e-9,
): void {
  assert.ok(
    Math.abs(actual.x - expected.x) <= epsilon,
    `x: expected ${expected.x}, received ${actual.x}`,
  );
  assert.ok(
    Math.abs(actual.y - expected.y) <= epsilon,
    `y: expected ${expected.y}, received ${actual.y}`,
  );
}

function expectRejection(
  operation: () => void,
  code: PlanRejectionCode,
  layerIndex?: number,
): OperationPlanRejectedError {
  try {
    operation();
  } catch (error) {
    assert.ok(error instanceof OperationPlanRejectedError, `unexpected error: ${String(error)}`);
    assert.equal(error.code, code);
    if (layerIndex !== undefined) {
      assert.equal(error.layerIndex, layerIndex);
    }
    return error;
  }
  assert.fail(`expected rejection with ${code}`);
}

function createNestedBrowser() {
  const browser = new SimulatedBrowser({ width: 1200, height: 900 });
  const outer = browser.createFrame(browser.root, {
    name: "outer",
    rect: { x: 100, y: 50, width: 668, height: 497 },
    viewport: { width: 640, height: 480 },
    border: { top: 8, right: 6, bottom: 4, left: 10 },
    padding: { top: 3, right: 7, bottom: 2, left: 5 },
    transform: scaling(1.5, 1.5),
    transformOrigin: { x: 0, y: 0 },
  });
  const inner = browser.createFrame(outer.frame.document, {
    name: "inner",
    rect: { x: 80, y: 60, width: 330, height: 260 },
    viewport: { width: 320, height: 240 },
    border: { top: 6, right: 2, bottom: 8, left: 4 },
    padding: { top: 2, right: 3, bottom: 4, left: 1 },
    transform: scaling(1.25, 0.8),
    transformOrigin: { x: 0, y: 0 },
  });
  const target = browser.createTarget(inner.frame.document, {
    elementId: "target",
    rect: { x: 40, y: 50, width: 100, height: 60 },
    transform: scaling(2, 1.5),
    transformOrigin: { x: 0, y: 0 },
  });
  return { browser, outer, inner, target };
}

test("cross-layer scroll, borders, and transforms resolve to the final click point", () => {
  const { browser, inner } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  assertPoint(captured.initialClickPoint, { x: 514.375, y: 280.1 });

  browser.scrollTo(browser.root, 10, 15);
  browser.scrollTo(browser.root.frames[0].frame.document, 30, 20);
  browser.scrollTo(inner.frame.document, 12, 6);

  const receipt = executeOperationPlan(browser, captured.plan);
  assertPoint(receipt.clickedAt, { x: 436.875, y: 227.9 });
  assert.equal(browser.clicks.length, 1);
  assertPoint(browser.clicks[0].point, { x: 436.875, y: 227.9 });
  assert.equal(browser.clicks[0].targetId, "target");
});

test("a 90 degree iframe transform is applied around its border-box origin", () => {
  const browser = new SimulatedBrowser({ width: 1000, height: 1000 });
  const frame = browser.createFrame(browser.root, {
    name: "rotated",
    rect: { x: 200, y: 100, width: 310, height: 210 },
    viewport: { width: 300, height: 200 },
    border: { top: 5, right: 5, bottom: 5, left: 5 },
    transform: rotationDegrees(90),
    transformOrigin: { x: 0, y: 0 },
  });
  browser.createTarget(frame.frame.document, {
    elementId: "corner",
    rect: { x: 0, y: 0, width: 20, height: 20 },
  });

  const captured = captureOperationPlan(browser, {
    framePath: ["rotated"],
    targetElementId: "corner",
  });
  assertPoint(captured.initialClickPoint, { x: 185, y: 115 });

  const receipt = executeOperationPlan(browser, captured.plan);
  assertPoint(receipt.clickedAt, { x: 185, y: 115 });
  assert.equal(browser.clicks.length, 1);
});

test("top-level navigation rejects the whole step without clicking", () => {
  const { browser } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.navigateTopLevel();
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "TOP_DOCUMENT_CHANGED",
  );
  assert.equal(browser.clicks.length, 0);
});

test("navigating an inner frame document rejects at that layer", () => {
  const { browser, inner } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.navigateFrame(inner.frame);
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "FRAME_DOCUMENT_CHANGED",
    1,
  );
  assert.equal(browser.clicks.length, 0);
});

test("a same-name replacement iframe is never adopted", () => {
  const { browser, outer } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  const replacement = browser.replaceFrame(outer.frame.document, "inner");
  browser.createTarget(replacement.frame.document, {
    elementId: "target",
    rect: { x: 40, y: 50, width: 100, height: 60 },
    transform: scaling(2, 1.5),
    transformOrigin: { x: 0, y: 0 },
  });

  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "FRAME_ELEMENT_DETACHED",
    1,
  );
  assert.equal(browser.clicks.length, 0);
});

test("a replaced target element rejects the step", () => {
  const { browser, inner, target } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.removeTarget(target);
  browser.createTarget(inner.frame.document, {
    elementId: "target",
    rect: { x: 40, y: 50, width: 100, height: 60 },
    transform: scaling(2, 1.5),
    transformOrigin: { x: 0, y: 0 },
  });

  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "TARGET_ELEMENT_REMOVED",
  );
  assert.equal(browser.clicks.length, 0);
});

test("scrolling the target outside its frame viewport rejects with zero clicks", () => {
  const { browser, inner } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.scrollTo(inner.frame.document, 200, 0);
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "POINT_OUTSIDE_VIEWPORT",
  );
  assert.equal(browser.clicks.length, 0);
});

test("a changed per-layer visible clip rejects with zero clicks", () => {
  const { browser, inner } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  inner.clipInParentDocument = { x: 400, y: 0, width: 20, height: 20 };
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "FRAME_POINT_CLIPPED",
    1,
  );
  assert.equal(browser.clicks.length, 0);
});

test("a target clipped inside its document rejects with zero clicks", () => {
  const { browser, target } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  target.clipInDocument = { x: 0, y: 0, width: 10, height: 10 };
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "TARGET_POINT_CLIPPED",
  );
  assert.equal(browser.clicks.length, 0);
});

test("navigation after preflight but before dispatch is a zero-click race", () => {
  const { browser } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.onBeforeDispatch = () => {
    browser.navigateTopLevel();
  };
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "EXECUTION_RACE_DETECTED",
  );
  assert.equal(browser.clicks.length, 0);
});

test("scroll after preflight invalidates coordinates before the click is recorded", () => {
  const { browser } = createNestedBrowser();
  const captured = captureOperationPlan(browser, {
    framePath: ["outer", "inner"],
    targetElementId: "target",
  });

  browser.onBeforeDispatch = () => {
    browser.scrollTo(browser.root, 25, 40);
  };
  expectRejection(
    () => executeOperationPlan(browser, captured.plan),
    "EXECUTION_RACE_DETECTED",
  );
  assert.equal(browser.clicks.length, 0);

  browser.onBeforeDispatch = null;
  browser.scrollTo(browser.root, 0, 0);
  const receipt = executeOperationPlan(browser, captured.plan);
  assertPoint(receipt.clickedAt, { x: 514.375, y: 280.1 });
  assert.equal(browser.clicks.length, 1);
});
