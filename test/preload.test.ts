import assert from "node:assert/strict";
import test from "node:test";

import {
  parseEarlyHintLinks,
  PreloadHintStore,
  type TagRequest,
} from "../src/index.ts";

const PAGE = "https://page.example.com/";
const SELF = "https://page.example.com";

function makeClock(start = 1_000_000) {
  let t = start;
  return {
    now: () => t,
    advance(ms: number) {
      t += ms;
    },
  };
}

function scriptTag(overrides: Partial<TagRequest> = {}): TagRequest {
  return {
    url: "https://cdn.example.com/app.js",
    as: "script",
    crossOrigin: "anonymous",
    integrity: "sha384-abc",
    ...overrides,
  };
}

function storeWithScriptHint(clock: { now: () => number }) {
  const store = new PreloadHintStore({ now: clock.now });
  store.addEarlyHints(
    '</app.js>; rel=preload; as=script; crossorigin="anonymous"; integrity="sha384-abc"',
    "https://cdn.example.com/",
  );
  return store;
}

test("parses 103 Link preload hints with url, as, crossorigin, integrity", () => {
  const hints = parseEarlyHintLinks(
    '</a.js>; rel=preload; as=script; crossorigin="use-credentials"; integrity="sha384-x", ' +
      '</b.css>; rel=preload; as=style, </img.png>; rel=preload; as=image; crossorigin, ' +
      '</f.woff2>; rel=preload; as=font, </skip>; rel=preconnect',
    PAGE,
  );
  assert.equal(hints.length, 4);
  assert.deepEqual(hints[0], {
    url: "https://page.example.com/a.js",
    as: "script",
    crossOrigin: "use-credentials",
    integrity: "sha384-x",
    recordedAt: hints[0].recordedAt,
  });
  assert.equal(hints[1].as, "style");
  assert.equal(hints[1].crossOrigin, null);
  assert.equal(hints[2].crossOrigin, "anonymous");
  assert.equal(hints[3].as, "font");
});

test("reuses hint when CSP allows and attributes match", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({
    status: 200,
    cspHeaders: ["script-src https://cdn.example.com; style-src 'self'"],
  });
  const decision = store.decide(scriptTag(), SELF);
  assert.equal(decision.reuse, true);
  // Hint is consumed.
  assert.equal(store.decide(scriptTag(), SELF).reuse, false);
});

test("discards hint when final CSP blocks the URL for its directive", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({
    status: 200,
    cspHeaders: ["script-src 'self'"],
  });
  const decision = store.decide(scriptTag(), SELF);
  assert.deepEqual(decision, { reuse: false, reason: "csp-blocked" });
  assert.equal(store.size, 0);
});

test("CSP falls back to default-src and honors 'none'", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({
    status: 200,
    cspHeaders: ["default-src 'none'; script-src https://cdn.example.com"],
  });
  assert.equal(store.decide(scriptTag(), SELF).reuse, true);

  const store2 = storeWithScriptHint(makeClock());
  store2.onFinalResponse({
    status: 200,
    cspHeaders: ["default-src 'self'"],
  });
  assert.deepEqual(store2.decide(scriptTag(), SELF), {
    reuse: false,
    reason: "csp-blocked",
  });
});

test("every enforced CSP policy must allow the fetch", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({
    status: 200,
    cspHeaders: [
      "script-src https://cdn.example.com",
      "script-src 'self'",
    ],
  });
  assert.deepEqual(store.decide(scriptTag(), SELF), {
    reuse: false,
    reason: "csp-blocked",
  });
});

test("discards hint on crossorigin mismatch", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({ status: 200 });
  const decision = store.decide(
    scriptTag({ crossOrigin: "use-credentials" }),
    SELF,
  );
  assert.deepEqual(decision, { reuse: false, reason: "crossorigin-mismatch" });
  assert.equal(store.size, 0);
});

test("discards hint on integrity mismatch", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({ status: 200 });
  const decision = store.decide(scriptTag({ integrity: "sha384-evil" }), SELF);
  assert.deepEqual(decision, { reuse: false, reason: "integrity-mismatch" });
  assert.equal(store.size, 0);
});

test("redirect final response clears all hints", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({ status: 302 });
  assert.equal(store.size, 0);
  assert.deepEqual(store.decide(scriptTag(), SELF), {
    reuse: false,
    reason: "no-hint",
  });
});

test("non-2xx final response clears all hints", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({ status: 404 });
  assert.equal(store.size, 0);

  const store500 = storeWithScriptHint(makeClock());
  store500.onFinalResponse({ status: 500 });
  assert.equal(store500.size, 0);
});

test("hints expire after 30 seconds unused", () => {
  const clock = makeClock();
  const store = storeWithScriptHint(clock);
  store.onFinalResponse({ status: 200 });

  clock.advance(29_999);
  assert.equal(store.decide(scriptTag(), SELF).reuse, true);

  const store2 = storeWithScriptHint(clock);
  store2.onFinalResponse({ status: 200 });
  clock.advance(30_001);
  assert.deepEqual(store2.decide(scriptTag(), SELF), {
    reuse: false,
    reason: "no-hint",
  });
  assert.equal(store2.size, 0);
});

test("font and image hints map to font-src and img-src", () => {
  const clock = makeClock();
  const store = new PreloadHintStore({ now: clock.now });
  store.addEarlyHints(
    '</f.woff2>; rel=preload; as=font; crossorigin, </i.png>; rel=preload; as=image',
    SELF,
  );
  store.onFinalResponse({
    status: 200,
    cspHeaders: ["font-src 'self'; img-src https:"],
  });
  assert.equal(
    store.decide(
      {
        url: "https://page.example.com/f.woff2",
        as: "font",
        crossOrigin: "anonymous",
        integrity: null,
      },
      SELF,
    ).reuse,
    true,
  );
  assert.equal(
    store.decide(
      {
        url: "https://page.example.com/i.png",
        as: "image",
        crossOrigin: null,
        integrity: null,
      },
      SELF,
    ).reuse,
    true,
  );
});
