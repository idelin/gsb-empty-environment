import assert from "node:assert/strict";
import test from "node:test";

import {
  ClientHints,
  LOW_ENTROPY_HINTS,
  attachClientHints,
  parseAcceptCH,
  parsePermissionsPolicy,
} from "./index.ts";

const TOP_LEVEL = "https://app.example";
const THIRD_PARTY = "https://widget.example";

const values = {
  "Sec-CH-UA": '"Chromium";v="131", "Not_A Brand";v="24"',
  "Sec-CH-UA-Mobile": "?0",
  "Sec-CH-UA-Platform": '"macOS"',
  "Sec-CH-UA-Full-Version-List":
    '"Chromium";v="131.0.6778.86", "Not_A Brand";v="24.0.0.0"',
  "Sec-CH-UA-Model": '"MacBookPro18,3"',
};

function topLevelNavigation(
  hints: ClientHints,
  url: string,
  acceptCH: string,
  permissionsPolicy?: string,
): boolean {
  return hints.observeResponse({
    url,
    isTopLevelNavigation: true,
    headers: {
      "Accept-CH": acceptCH,
      ...(permissionsPolicy
        ? { "Permissions-Policy": permissionsPolicy }
        : {}),
    },
  });
}

test("low-entropy hints go to same-origin requests by default", () => {
  const hints = new ClientHints({ hints: values });
  assert.equal(topLevelNavigation(hints, `${TOP_LEVEL}/`, ""), true);

  const headers = hints.getRequestHeaders({
    url: `${TOP_LEVEL}/api/me`,
    topLevelOrigin: TOP_LEVEL,
  });

  assert.deepEqual(headers, {
    "Sec-CH-UA": values["Sec-CH-UA"],
    "Sec-CH-UA-Mobile": values["Sec-CH-UA-Mobile"],
    "Sec-CH-UA-Platform": values["Sec-CH-UA-Platform"],
  });
  for (const hint of LOW_ENTROPY_HINTS) {
    assert.ok(hint in headers);
  }
});

test("high-entropy hints require an HTTPS top-level Accept-CH opt-in", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(
    hints,
    `${TOP_LEVEL}/`,
    "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Model",
  );

  assert.deepEqual(
    hints.getRequestHeaders({
      url: `${TOP_LEVEL}/same-origin`,
      topLevelOrigin: TOP_LEVEL,
    }),
    values,
  );
});

test("cross-origin requests do not receive high-entropy hints without delegation", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(
    hints,
    `${TOP_LEVEL}/`,
    "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Model",
    "ch-ua-model=(self)",
  );

  const headers = hints.getRequestHeaders({
    url: `${THIRD_PARTY}/embed`,
    topLevelOrigin: TOP_LEVEL,
  });

  assert.deepEqual(headers, {});
});

test("a Permissions-Policy allowlist containing the target permits its high-entropy hint", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(
    hints,
    `${TOP_LEVEL}/`,
    "Sec-CH-UA-Full-Version-List, Sec-CH-UA-Model",
    `ch-ua-model=(self "${THIRD_PARTY}"), ch-ua-full-version-list=(self)`,
  );

  const headers = hints.getRequestHeaders({
    url: `${THIRD_PARTY}/embed`,
    topLevelOrigin: TOP_LEVEL,
  });

  assert.equal(headers["Sec-CH-UA-Model"], values["Sec-CH-UA-Model"]);
  assert.equal(headers["Sec-CH-UA-Full-Version-List"], undefined);
});

test("a wildcard Permissions-Policy allowlist delegates to any origin", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(
    hints,
    `${TOP_LEVEL}/`,
    "Sec-CH-UA-Model",
    "ch-ua-model=*",
  );

  assert.equal(
    hints.getRequestHeaders({
      url: "https://another.example/frame",
      topLevelOrigin: TOP_LEVEL,
    })["Sec-CH-UA-Model"],
    values["Sec-CH-UA-Model"],
  );
});

test("Accept-CH on a plaintext top-level response is ignored", () => {
  const hints = new ClientHints({ hints: values });
  assert.equal(
    topLevelNavigation(
      hints,
      "http://app.example/",
      "Sec-CH-UA-Model",
      "ch-ua-model=*",
    ),
    false,
  );

  const headers = hints.getRequestHeaders({
    url: "http://app.example/page",
    topLevelOrigin: "http://app.example",
  });
  assert.equal(headers["Sec-CH-UA-Model"], undefined);
});

test("a plaintext response cannot reuse an earlier opt-in for the same origin", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(hints, `${TOP_LEVEL}/secure`, "Sec-CH-UA-Model");
  topLevelNavigation(hints, `${TOP_LEVEL}/plain`, "", "");

  assert.equal(
    hints.observeResponse({
      url: `${TOP_LEVEL}/downgraded`,
      isTopLevelNavigation: true,
      headers: { "Accept-CH": "Sec-CH-UA-Model" },
      secure: false,
    }),
    false,
  );
  assert.equal(
    hints.getRequestHeaders({
      url: `${TOP_LEVEL}/after-downgrade`,
      topLevelOrigin: TOP_LEVEL,
    })["Sec-CH-UA-Model"],
    undefined,
  );
});

test("Accept-CH on a subresource response is ignored", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(hints, `${TOP_LEVEL}/`, "Sec-CH-UA");

  assert.equal(
    hints.observeResponse({
      url: `${TOP_LEVEL}/image.png`,
      isTopLevelNavigation: false,
      headers: {
        "Accept-CH": "Sec-CH-UA-Model",
        "Permissions-Policy": "ch-ua-model=*",
      },
    }),
    false,
  );
  assert.equal(
    hints.getRequestHeaders({
      url: `${TOP_LEVEL}/image.png`,
      topLevelOrigin: TOP_LEVEL,
    })["Sec-CH-UA-Model"],
    undefined,
  );
});

test("a subresource cannot rewrite the active document permissions policy", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(
    hints,
    `${TOP_LEVEL}/`,
    "Sec-CH-UA-Model",
    "ch-ua-model=(self)",
  );
  hints.observeResponse({
    url: `${THIRD_PARTY}/script.js`,
    isTopLevelNavigation: false,
    headers: {
      "Accept-CH": "Sec-CH-UA-Model",
      "Permissions-Policy": "ch-ua-model=*",
    },
  });

  assert.equal(
    hints.getRequestHeaders({
      url: `${THIRD_PARTY}/api`,
      topLevelOrigin: TOP_LEVEL,
    })["Sec-CH-UA-Model"],
    undefined,
  );
});

test("the last top-level navigation is the default document context", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(hints, `${TOP_LEVEL}/`, "Sec-CH-UA-Model");

  assert.equal(
    hints.getRequestHeaders({ url: `${TOP_LEVEL}/resource` })[
      "Sec-CH-UA-Model"
    ],
    values["Sec-CH-UA-Model"],
  );
});

test("attachClientHints works in either argument order", () => {
  const hints = new ClientHints({ hints: values });
  topLevelNavigation(hints, `${TOP_LEVEL}/`, "Sec-CH-UA-Model");
  const request = { url: `${TOP_LEVEL}/resource`, topLevelOrigin: TOP_LEVEL };

  assert.deepEqual(
    attachClientHints(hints, request),
    attachClientHints(request, hints),
  );
});

test("Accept-CH parsing is case-insensitive and ignores malformed tokens", () => {
  assert.deepEqual(
    parseAcceptCH(
      "sec-ch-ua-model, SEC-CH-UA-FULL-VERSION-LIST, *, not-a-hint",
    ),
    ["Sec-CH-UA-Model", "Sec-CH-UA-Full-Version-List"],
  );
});

test("Map-based response headers are read case-insensitively", () => {
  const hints = new ClientHints({ hints: values });
  hints.observeResponse({
    url: `${TOP_LEVEL}/`,
    isTopLevelNavigation: true,
    headers: new Map([
      ["accept-ch", "Sec-CH-UA-Model"],
      ["permissions-policy", `ch-ua-model=("${THIRD_PARTY}")`],
    ]),
  });

  assert.equal(
    hints.getRequestHeaders({
      url: `${THIRD_PARTY}/api`,
      topLevelOrigin: TOP_LEVEL,
    })["Sec-CH-UA-Model"],
    values["Sec-CH-UA-Model"],
  );
});

test("Permissions-Policy parsing handles self, quoted origins, and wildcards", () => {
  const policy = parsePermissionsPolicy(
    'ch-ua-model=(self "https://widget.example"), ch-ua-platform-version=*',
  );

  assert.deepEqual(policy["ch-ua-model"], {
    any: false,
    self: true,
    origins: ["https://widget.example"],
  });
  assert.equal(policy["ch-ua-platform-version"].any, true);
});
