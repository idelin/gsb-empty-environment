import assert from "node:assert/strict";
import test from "node:test";
import {
  decideLinkRel,
  decideWindowOpen,
  hasNoopener,
  type WindowOpenInput,
} from "../src/noopener.ts";

interface SimulatedPage {
  name: string;
}

interface SimulatedWindow {
  opener: SimulatedPage | null;
}

function openWindow(caller: SimulatedPage, input: WindowOpenInput): SimulatedWindow {
  const decision = decideWindowOpen(input);
  const severed = hasNoopener(decision.value);
  return { opener: severed ? null : caller };
}

test("link target=_blank without rel gains noopener", () => {
  const decision = decideLinkRel({ target: "_blank" });
  assert.equal(decision.value, "noopener");
  assert.equal(decision.changed, true);
});

test("link target=_blank merges noopener into existing rel", () => {
  const decision = decideLinkRel({ target: "_blank", rel: "nofollow" });
  assert.equal(decision.value, "nofollow noopener");
});

test("link with existing noopener is left untouched, nothing else added", () => {
  const decision = decideLinkRel({ target: "_blank", rel: "noopener" });
  assert.equal(decision.value, "noopener");
  assert.equal(decision.changed, false);
});

test("link with non-blank target is left untouched", () => {
  for (const target of ["_self", "_parent", "_top", "docs", undefined]) {
    const decision = decideLinkRel({ target });
    assert.equal(decision.changed, false, target);
    assert.equal(decision.value, "");
  }
});

test("window.open target=_blank gains noopener in features", () => {
  const decision = decideWindowOpen({ target: "_blank", features: "width=600,height=400" });
  assert.equal(decision.value, "width=600,height=400,noopener");
  assert.equal(decision.changed, true);
});

test("window.open with existing noopener gets no extra tokens", () => {
  const decision = decideWindowOpen({ target: "_blank", features: "noopener,width=600" });
  assert.equal(decision.value, "noopener,width=600");
  assert.equal(decision.changed, false);
});

test("named window with caller keepOpener marker follows the caller", () => {
  const decision = decideWindowOpen({ target: "docs", keepOpener: true });
  assert.equal(decision.changed, false);
  assert.equal(decision.openerKept, true);
  assert.equal(decision.value, "");
});

test("named window without marker is severed by default", () => {
  const decision = decideWindowOpen({ target: "docs" });
  assert.equal(decision.value, "noopener");
  assert.equal(decision.openerKept, false);
});

test("self-navigation targets are left untouched", () => {
  for (const target of ["_self", "_parent", "_top", undefined]) {
    const decision = decideWindowOpen({ target });
    assert.equal(decision.changed, false, target);
  }
});

test("opens _blank and a named window once each; opener is null for both", () => {
  const source: SimulatedPage = { name: "source" };
  const blankChild = openWindow(source, { target: "_blank" });
  const namedChild = openWindow(source, { target: "docs" });
  assert.equal(blankChild.opener, null);
  assert.equal(namedChild.opener, null);
});

test("named window opened with keepOpener marker retains opener", () => {
  const source: SimulatedPage = { name: "source" };
  const child = openWindow(source, { target: "docs", keepOpener: true });
  assert.equal(child.opener, source);
});
