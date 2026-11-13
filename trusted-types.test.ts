import { test } from "node:test";
import assert from "node:assert/strict";

import {
  TrustedHTML,
  TrustedScript,
  TrustedScriptURL,
  TrustedTypePolicyFactory,
  TrustedTypeEnforcer,
  DocumentPipeline,
  createPipeline,
  DEFAULT_POLICY_NAME,
  isHTML,
  isScript,
  isScriptURL,
} from "./index.ts";

function fresh(): {
  factory: TrustedTypePolicyFactory;
  enforcer: TrustedTypeEnforcer;
  pipeline: DocumentPipeline;
} {
  return createPipeline(new TrustedTypePolicyFactory());
}

test("the three results carry distinct, non-interchangeable brands", () => {
  const { factory } = fresh();
  const policy = factory.createPolicy("p", {
    createHTML: (input) => input,
    createScript: (input) => input,
    createScriptURL: (input) => input,
  });

  const html = policy.createHTML("<b>hi</b>");
  const script = policy.createScript("let a = 1;");
  const url = policy.createScriptURL("https://example.com/a.js");

  assert.ok(html instanceof TrustedHTML);
  assert.ok(script instanceof TrustedScript);
  assert.ok(url instanceof TrustedScriptURL);

  assert.ok(isHTML(html) && !isScript(html) && !isScriptURL(html));
  assert.ok(!isHTML(url) && !isScript(url) && isScriptURL(url));
  assert.equal(html.toString(), "<b>hi</b>");
});

test("a plain string is not a trusted result", () => {
  assert.ok(!isHTML("<b></b>"));
  assert.ok(!isScriptURL("https://example.com/a.js"));
});

test("createHTML result is allowed in innerHTML when it has no event attributes", () => {
  const { factory, pipeline } = fresh();
  const policy = factory.createPolicy("sanitizer");
  const html = policy.createHTML("<div><p>hello</p></div>");

  const result = pipeline.writeInnerHTML(html);
  assert.equal(result.innerHTML, "<div><p>hello</p></div>");
});

test("innerHTML rejects markup carrying an event-handler attribute", () => {
  const { factory, pipeline } = fresh();
  const policy = factory.createPolicy("sanitizer");

  for (const markup of [
    '<img src=x onerror="alert(1)">',
    "<div ONCLICK='x()'>tap</div>",
    '<button onmouseover=go>go</button>',
  ]) {
    const html = policy.createHTML(markup);
    assert.throws(() => pipeline.writeInnerHTML(html), /event-handler attribute/);
  }
});

test("ScriptURL product handed to innerHTML is rejected (the required check)", () => {
  const { factory, enforcer, pipeline } = fresh();
  const policy = factory.createPolicy("loader");
  const url = policy.createScriptURL("https://example.com/a.js");

  assert.ok(url instanceof TrustedScriptURL);
  assert.throws(() => enforcer.setHTML(url), /requires TrustedHTML, received TrustedScriptURL/);
  assert.throws(() => pipeline.writeInnerHTML(url), /cannot be swapped between sinks/);
});

test("HTML and Script products are rejected at the script-address sink", () => {
  const { factory, enforcer } = fresh();
  const policy = factory.createPolicy("p");

  assert.throws(
    () => enforcer.setScriptURL(policy.createHTML("<b></b>")),
    /requires TrustedScriptURL, received TrustedHTML/,
  );
  assert.throws(
    () => enforcer.setScriptURL(policy.createScript("let x = 1;")),
    /requires TrustedScriptURL, received TrustedScript/,
  );
});

test("Script product is rejected at the innerHTML sink", () => {
  const { factory, enforcer } = fresh();
  const policy = factory.createPolicy("p");
  assert.throws(
    () => enforcer.setHTML(policy.createScript("alert(1)")),
    /requires TrustedHTML, received TrustedScript/,
  );
});

test("createScriptURL rejects javascript: and data: URLs", () => {
  const { factory } = fresh();
  const policy = factory.createPolicy("loader");

  for (const bad of [
    "javascript:alert(1)",
    "  JAVASCRIPT:alert(1)",
    "data:text/javascript,alert(1)",
    "data:application/javascript;base64,YWxlcnQoMSk=",
  ]) {
    assert.throws(() => policy.createScriptURL(bad), /scheme is not allowed/);
  }

  for (const good of [
    "https://example.com/a.js",
    "/static/a.js",
    "//cdn.example.com/a.js",
    "blob:https://example.com/abcd",
  ]) {
    assert.doesNotThrow(() => policy.createScriptURL(good));
    assert.equal(policy.createScriptURL(good).toString(), good);
  }
});

test("a policy cannot rewrite a forbidden scheme back into a ScriptURL", () => {
  const { factory } = fresh();
  const policy = factory.createPolicy("loader", {
    createScriptURL: () => "javascript:alert(1)",
  });
  assert.throws(() => policy.createScriptURL("https://safe.example.com/a.js"), /scheme/);
});

test("the default policy upgrades strings only when no named policy exists", () => {
  const factory = new TrustedTypePolicyFactory();
  const enforcer = new TrustedTypeEnforcer(factory);

  assert.throws(
    () => enforcer.setHTML("<p>no policy</p>"),
    /no "default" policy/,
  );

  factory.createPolicy(DEFAULT_POLICY_NAME, {
    createHTML: (input) => input.replace(/</g, "&lt;"),
  });
  assert.ok(factory.defaultPolicy !== null);
  assert.equal(enforcer.setHTML("<p>x</p>"), "&lt;p>x&lt;/p>");

  factory.createPolicy("named");
  assert.equal(factory.defaultPolicy, null);
  assert.throws(() => enforcer.setHTML("<p>x</p>"), /no "default" policy/);
});

test("default-policy string that still contains an event attribute is refused", () => {
  const factory = new TrustedTypePolicyFactory();
  const enforcer = new TrustedTypeEnforcer(factory);
  factory.createPolicy(DEFAULT_POLICY_NAME, { createHTML: (input) => input });

  assert.throws(
    () => enforcer.setHTML('<img src=x onerror="alert(1)">'),
    /event-handler attribute/,
  );
});

test("default policy producing a forbidden script scheme is rejected at the URL sink", () => {
  const factory = new TrustedTypePolicyFactory();
  const enforcer = new TrustedTypeEnforcer(factory);
  factory.createPolicy(DEFAULT_POLICY_NAME, {
    createScriptURL: (input) => input,
  });

  assert.throws(() => enforcer.setScriptURL("javascript:alert(1)"), /scheme/);
  assert.equal(
    enforcer.setScriptURL("https://example.com/a.js"),
    "https://example.com/a.js",
  );
});

test("duplicate policy names are rejected", () => {
  const { factory } = fresh();
  factory.createPolicy("once");
  assert.throws(() => factory.createPolicy("once"), /already exists/);

  factory.createPolicy(DEFAULT_POLICY_NAME);
  assert.throws(() => factory.createPolicy(DEFAULT_POLICY_NAME), /already exists/);
});

test("pipeline routes each brand to its matching sink", () => {
  const { factory, pipeline } = fresh();
  const policy = factory.createPolicy("p");

  const afterHTML = pipeline.writeInnerHTML(policy.createHTML("<span>ok</span>"));
  assert.equal(afterHTML.innerHTML, "<span>ok</span>");

  const afterScript = pipeline.writeScript(policy.createScript("var n = 2;"));
  assert.equal(afterScript.scriptText, "var n = 2;");

  const afterURL = pipeline.writeScriptURL(
    policy.createScriptURL("https://example.com/a.js"),
  );
  assert.equal(afterURL.scriptSrc, "https://example.com/a.js");
});

test("emptyHTML and emptyScript are stable empty branded values", () => {
  const { factory, enforcer } = fresh();
  assert.ok(factory.emptyHTML instanceof TrustedHTML);
  assert.ok(factory.emptyScript instanceof TrustedScript);
  assert.equal(factory.emptyHTML.toString(), "");
  assert.equal(factory.emptyScript.toString(), "");
  assert.equal(enforcer.setHTML(factory.emptyHTML), "");
});
