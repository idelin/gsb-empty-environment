import assert from "node:assert/strict";
import test from "node:test";
import {
  findPageAnchor,
  selectUniqueStructuralPath,
} from "../src/dom.ts";
import {
  FakeElement,
  buildDocument,
} from "./helpers.ts";

test("structural selection ignores IDs, names, classes and attributes", () => {
  const main = new FakeElement("main", {
    id: "extension-config",
    className: "trusted-looking",
    name: "allowedOrigins",
  });
  const { document } = buildDocument([main]);

  const result = selectUniqueStructuralPath(document, ["html", "body", "main"]);
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.element, main);
  }
});

test("duplicate or conflicting structural candidates are rejected", () => {
  const legitimate = new FakeElement("main", { id: "app" });
  const attacker = new FakeElement("main", {
    id: "extension-config",
    name: "extension_security",
  });
  const { document } = buildDocument([legitimate, attacker]);

  const result = findPageAnchor(document);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.reason, "ambiguous");
    assert.equal(result.depth, 2);
  }
});

test("missing anchors and malformed structural paths fail closed", () => {
  const { document } = buildDocument([]);
  const missing = findPageAnchor(document);
  assert.equal(missing.ok, false);
  if (!missing.ok) {
    assert.equal(missing.reason, "missing");
  }

  assert.equal(selectUniqueStructuralPath(document, []).ok, false);
  assert.equal(
    selectUniqueStructuralPath(document, ["#app"]).ok,
    false,
  );
});

test("a page-supplied deeper decoy cannot change the fixed path", () => {
  const decoy = new FakeElement("main", { id: "extension-config" }).append(
    new FakeElement("section"),
  );
  const { body, document } = buildDocument([]);
  body.append(new FakeElement("div").append(decoy));

  assert.equal(findPageAnchor(document).ok, false);
  assert.equal(
    selectUniqueStructuralPath(document, ["html", "body", "div", "main"]).ok,
    true,
  );
});
