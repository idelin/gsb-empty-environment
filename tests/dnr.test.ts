import test from "node:test";
import assert from "node:assert/strict";
import {
  InvalidDnrRuleError,
  evaluateDeclarativeNetRequest,
  findDnrPriorityIssues,
  parseDnrRules,
  urlFilterToRegExp,
  type DnrRule,
} from "../src/declarative-net-request.ts";

const request = {
  url: "https://bank.example/account",
  initiator: "https://attacker.example/page",
  method: "GET",
  type: "main_frame" as const,
  tabId: 7,
};

function rule(id: number, type: DnrRule["action"]["type"], extra: Partial<DnrRule> = {}): DnrRule {
  return {
    id,
    priority: extra.priority ?? 1,
    action:
      type === "redirect"
        ? { type, redirect: { url: "https://redirect.example/" } }
        : type === "modifyHeaders"
          ? { type, requestHeaders: [{ header: "x-test", operation: "set", value: "1" }] }
          : { type },
    condition: extra.condition ?? { urlFilter: "*" },
  };
}

test("same-priority action precedence is allowAllRequests > allow > block > redirect > modifyHeaders", () => {
  const rules = [
    rule(1, "modifyHeaders"),
    rule(2, "redirect"),
    rule(3, "block"),
    rule(4, "allow"),
    rule(5, "allowAllRequests"),
  ];
  assert.equal(evaluateDeclarativeNetRequest(rules, request).action, "allowAllRequests");

  let result = evaluateDeclarativeNetRequest(rules.filter((entry) => entry.id !== 5), request);
  assert.equal(result.action, "allow");
  assert.equal(result.allowed, true);

  result = evaluateDeclarativeNetRequest(rules.filter((entry) => entry.id < 4 || entry.id === 1), request);
  assert.equal(result.action, "block");
  assert.equal(result.blocked, true);

  result = evaluateDeclarativeNetRequest(rules.filter((entry) => entry.id === 1 || entry.id === 2), request);
  assert.equal(result.action, "redirect");
  assert.equal(result.redirect?.url, "https://redirect.example/");

  result = evaluateDeclarativeNetRequest(rules.filter((entry) => entry.id === 1), request);
  assert.equal(result.action, "modifyHeaders");
  assert.equal(result.blocked, false);
  assert.equal(result.modifyHeaderRules.length, 1);
});

test("higher numeric priority wins across action types", () => {
  const rules = [
    rule(1, "allow", { priority: 1 }),
    rule(2, "block", { priority: 5 }),
    rule(3, "redirect", { priority: 10 }),
  ];
  const result = evaluateDeclarativeNetRequest(rules, request);
  assert.equal(result.action, "redirect");
  assert.equal(result.winner?.rule.id, 3);
});

test("tie-break uses lower rule id after priority and action precedence", () => {
  const rules = [rule(8, "block"), rule(3, "block"), rule(5, "allow", { priority: 2 })];
  const result = evaluateDeclarativeNetRequest(rules, request);
  assert.equal(result.action, "allow");
  assert.equal(result.winner?.rule.id, 5);
});

test("allowAllRequests is ignored for non-document resource types", () => {
  const rules = [rule(1, "allowAllRequests"), rule(2, "block")];
  const result = evaluateDeclarativeNetRequest(rules, {
    ...request,
    type: "script",
  });
  assert.equal(result.action, "block");
});

test("modifyHeaders rules are reported separately even when a block wins", () => {
  const rules = [rule(1, "block", { priority: 3 }), rule(2, "modifyHeaders", { priority: 1 })];
  const result = evaluateDeclarativeNetRequest(rules, request);
  assert.equal(result.action, "block");
  assert.equal(result.modifyHeaderRules[0].rule.id, 2);
});

test("urlFilter implements domain, start, end and separator anchors", () => {
  assert.equal(urlFilterToRegExp("||bank.example^").test("https://bank.example/account"), true);
  assert.equal(urlFilterToRegExp("||bank.example^").test("https://app.bank.example/account"), true);
  assert.equal(urlFilterToRegExp("||bank.example^").test("https://bank.example.evil/account"), false);
  assert.equal(urlFilterToRegExp("|https://example.com/").test("https://example.com/a"), true);
  assert.equal(urlFilterToRegExp("|https://example.com/").test("https://sub.example.com/a"), false);
  assert.equal(urlFilterToRegExp("logo.png|").test("https://example.com/logo.png"), true);
  assert.equal(urlFilterToRegExp("*.png|").test("https://example.com/logo.png?x=1"), false);
  assert.equal(urlFilterToRegExp("foo*bar").test("https://example.com/foo/x/bar"), true);
});

test("condition fields include domains, methods, resource types and tab ids", () => {
  const rules: DnrRule[] = [
    {
      id: 1,
      priority: 1,
      action: { type: "block" },
      condition: {
        requestDomains: ["bank.example"],
        excludedRequestDomains: ["evil.example"],
        requestMethods: ["POST"],
        resourceTypes: ["xmlhttprequest"],
        tabIds: [7],
      },
    },
  ];
  assert.equal(
    evaluateDeclarativeNetRequest(rules, {
      url: "https://app.bank.example/api",
      initiator: "https://attacker.example/page",
      method: "post",
      type: "xmlhttprequest",
      tabId: 7,
    }).action,
    "block",
  );
  assert.equal(
    evaluateDeclarativeNetRequest(rules, {
      url: "https://bank.example/api",
      method: "GET",
      type: "xmlhttprequest",
      tabId: 7,
    }).action,
    "none",
  );
  assert.equal(
    evaluateDeclarativeNetRequest(rules, {
      url: "https://bank.example/api",
      method: "POST",
      type: "script",
      tabId: 7,
    }).action,
    "none",
  );
  assert.equal(
    evaluateDeclarativeNetRequest(rules, {
      url: "https://bank.example/api",
      method: "POST",
      type: "xmlhttprequest",
      tabId: 8,
    }).action,
    "none",
  );
});

test("regexFilter and initiator domains work", () => {
  const rules: DnrRule[] = [
    {
      id: 1,
      action: { type: "block" },
      condition: {
        regexFilter: "^https://bank\\.example/(account|transfer)",
        initiatorDomains: ["attacker.example"],
        excludedInitiatorDomains: ["trusted.example"],
      },
    },
  ];
  assert.equal(evaluateDeclarativeNetRequest(rules, request).action, "block");
  assert.equal(
    evaluateDeclarativeNetRequest(rules, { ...request, initiator: "https://trusted.example/page" }).action,
    "none",
  );
});

test("domainType handles missing initiator as first-party main frame", () => {
  const rules: DnrRule[] = [
    {
      id: 1,
      action: { type: "block" },
      condition: { domainType: "firstParty", urlFilter: "*" },
    },
    {
      id: 2,
      action: { type: "block" },
      condition: { domainType: "thirdParty", urlFilter: "*" },
    },
  ];
  const firstParty = evaluateDeclarativeNetRequest([rules[0]], {
    url: "https://bank.example/",
    type: "main_frame",
  });
  assert.equal(firstParty.action, "block");
  const thirdParty = evaluateDeclarativeNetRequest([rules[1]], {
    url: "https://bank.example/",
    type: "main_frame",
  });
  assert.equal(thirdParty.action, "none");
});

test("parseDnrRules rejects malformed rules and duplicate ids", () => {
  const invalid: unknown[] = [
    {},
    [{ id: 1 }],
    [{ id: 1, action: { type: "block" }, condition: {}, priority: 0 }],
    [{ id: 1, action: { type: "nope" }, condition: {} }],
    [{ id: 1, action: { type: "redirect" }, condition: {} }],
    [{ id: 1, action: { type: "modifyHeaders" }, condition: {} }],
    [{ id: 1, action: { type: "block" }, condition: { urlFilter: "x", regexFilter: "y" } }],
    [{ id: 1, action: { type: "block" }, condition: { resourceTypes: ["wat"] } }],
    [{ id: 1, action: { type: "block" }, condition: { regexFilter: "(" } }],
    [
      { id: 1, action: { type: "block" }, condition: {} },
      { id: 1, action: { type: "block" }, condition: {} },
    ],
  ];
  for (const input of invalid) {
    assert.throws(() => parseDnrRules(input), InvalidDnrRuleError, JSON.stringify(input));
  }
  assert.equal(parseDnrRules("[]").length, 0);
});

test("priority override analysis detects allow rules that can outrank blocks", () => {
  const rules = [
    rule(1, "block"),
    rule(2, "allow"),
    rule(3, "allowAllRequests"),
    { ...rule(4, "block"), condition: { urlFilter: "https://other.example/*" } },
    rule(5, "allow", { priority: 1, condition: { urlFilter: "https://low.example/*" } }),
    rule(6, "block", { priority: 9, condition: { urlFilter: "https://low.example/*" } }),
  ];
  const issues = findDnrPriorityIssues(rules);
  assert.equal(issues.some((entry) => entry.kind === "allow" && entry.overridingRuleId === 2), true);
  assert.equal(issues.some((entry) => entry.kind === "allowAllRequests" && entry.overridingRuleId === 3), true);
  assert.equal(issues.find((entry) => entry.overridingRuleId === 4), undefined);
  assert.equal(issues.find((entry) => entry.overridingRuleId === 5), undefined);
});
