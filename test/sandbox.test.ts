import test from "node:test";
import assert from "node:assert/strict";

import {
  SANDBOX_CAPABILITY_TOKENS,
  deriveFrameTree,
  derivePopup,
  deriveSandbox,
  parseSandboxAttribute,
} from "../index.ts";

const HOST = "https://host.example";
const HOST_FRAME = "https://host.example/frame";
const EVIL = "https://evil.example";

test("the package name resolves through its exports map", async () => {
  const imported = await import("netnew-20261112-0033");

  assert.equal(typeof imported.deriveFrameTree, "function");
  assert.equal(
    imported.SANDBOX_CAPABILITY_TOKENS.includes("allow-scripts"),
    true,
  );
});

test("parses and normalizes sandbox tokens", () => {
  const parsed = parseSandboxAttribute(
    "Allow-Scripts  allow-same-origin allow-popups-to-escape-sandbox nope",
  );

  assert.equal(parsed.present, true);
  assert.deepEqual(parsed.tokens, [
    "allow-scripts",
    "allow-same-origin",
  ]);
  assert.deepEqual(parsed.modifiers, [
    "allow-popups-to-escape-sandbox",
  ]);
  assert.deepEqual(parsed.unknownTokens, ["nope"]);

  const absent = parseSandboxAttribute(undefined);
  assert.equal(absent.present, false);
  assert.deepEqual(absent.tokens, []);

  const empty = parseSandboxAttribute("");
  assert.equal(empty.present, true);
  assert.deepEqual(empty.tokens, []);

  const array = parseSandboxAttribute([
    "allow-scripts",
    "allow-scripts",
    "ALLOW-FORMS",
  ]);
  assert.deepEqual(array.tokens, ["allow-scripts", "allow-forms"]);
});

test("nested frames intersect capabilities and explain blocked tokens", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        name: "outer",
        src: HOST_FRAME,
        sandbox: "allow-scripts",
        children: [
          {
            name: "inner",
            src: `${HOST}/inner`,
            sandbox: "allow-scripts allow-same-origin",
            children: [
              {
                name: "deep",
                src: `${HOST}/deep`,
              },
            ],
          },
        ],
      },
    ],
  });

  assert.equal(tree.sandboxActive, false);
  const outer = tree.children[0];
  const inner = outer.children[0];
  const deep = inner.children[0];

  assert.equal(outer.path, "root/iframe[0]");
  assert.equal(outer.sandboxActive, true);
  assert.deepEqual(outer.effectiveTokens, ["allow-scripts"]);
  assert.equal(outer.flags.sameOrigin, false);
  assert.equal(outer.flagResolutions.sameOrigin.source, "omitted");
  assert.equal(outer.origin.effective, "opaque");
  assert.equal(outer.sameOriginAsParent, false);
  assert.equal(outer.unsafe, false);

  // The child asks for allow-same-origin, but an ancestor removed it. Sandbox
  // restrictions accumulate, so a child cannot restore a capability.
  assert.deepEqual(inner.effectiveTokens, ["allow-scripts"]);
  assert.equal(inner.flags.sameOrigin, false);
  assert.equal(
    inner.flagResolutions.sameOrigin.source,
    "blocked-ancestor",
  );
  assert.equal(inner.origin.effective, "opaque");
  assert.equal(inner.unsafe, false);

  // An omitted attribute inherits exactly the parent's result.
  assert.equal(deep.sandboxAttributePresent, false);
  assert.equal(deep.sandboxActive, true);
  assert.deepEqual(deep.effectiveTokens, ["allow-scripts"]);
  assert.equal(deep.flagResolutions.scripts.source, "inherited");
  assert.equal(deep.flagResolutions.sameOrigin.source, "inherited");
  assert.equal(deep.flags.sameOrigin, false);
  assert.equal(deep.unsafe, false);

  assert.deepEqual(
    tree.popupEscapeTokenExplicit,
    false,
  );
  assert.equal(tree.popup.escaped, false);
});

test("same-origin frame with scripts and same-origin is unsafe", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox: "allow-scripts allow-same-origin",
      },
    ],
  });
  const frame = tree.children[0];

  assert.equal(frame.sandboxActive, true);
  assert.equal(frame.sameOriginAsParent, true);
  assert.equal(frame.origin.effective, "https://host.example");
  assert.equal(frame.origin.forcedOpaque, false);
  assert.equal(frame.flags.scripts, true);
  assert.equal(frame.flags.sameOrigin, true);
  assert.equal(frame.unsafe, true);
  assert.deepEqual(frame.unsafeReasons, [
    "same-origin-scripts-can-remove-sandbox",
  ]);
});

test("an inherited dual-token sandbox stays unsafe in a nested frame", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox: "allow-scripts allow-same-origin",
        children: [{ src: `${HOST}/inherits-attack` }],
      },
    ],
  });
  const outer = tree.children[0];
  const inner = outer.children[0];

  assert.equal(outer.unsafe, true);
  assert.equal(inner.sandboxAttributePresent, false);
  assert.equal(inner.sandboxActive, true);
  assert.equal(inner.flagResolutions.scripts.source, "inherited");
  assert.equal(inner.flagResolutions.sameOrigin.source, "inherited");
  assert.equal(inner.flags.scripts, true);
  assert.equal(inner.flags.sameOrigin, true);
  assert.equal(inner.sameOriginAsParent, true);
  assert.equal(inner.unsafe, true);
  assert.deepEqual(inner.unsafeReasons, [
    "same-origin-scripts-can-remove-sandbox",
  ]);
});

test("cross-origin frame with both tokens cannot reach the parent", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: `${EVIL}/attack`,
        sandbox: "allow-scripts allow-same-origin",
      },
    ],
  });
  const frame = tree.children[0];

  assert.equal(frame.flags.scripts, true);
  assert.equal(frame.flags.sameOrigin, true);
  assert.equal(frame.origin.effective, "https://evil.example");
  assert.equal(frame.sameOriginAsParent, false);
  assert.equal(frame.unsafe, false);
});

test("an unsandboxed same-origin iframe is not a sandbox escape", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [{ src: HOST_FRAME }],
  });
  const frame = tree.children[0];

  assert.equal(frame.sandboxAttributePresent, false);
  assert.equal(frame.sandboxActive, false);
  assert.equal(frame.sameOriginAsParent, true);
  assert.equal(frame.flags.scripts, true);
  assert.equal(frame.flags.sameOrigin, true);
  assert.equal(frame.unsafe, false);
  assert.deepEqual(frame.unsafeReasons, []);
});

test("srcdoc inherits the parent origin, not a child URL origin", () => {
  const direct = deriveFrameTree({
    origin: HOST,
    children: [
      {
        srcdoc: "<script>alert(1)</script>",
        sandbox: "allow-scripts",
      },
    ],
  }).children[0];

  assert.equal(direct.origin.source, "srcdoc");
  assert.equal(direct.origin.base, HOST);
  assert.equal(direct.origin.effective, "opaque");
  assert.equal(direct.origin.forcedOpaque, true);
  assert.equal(direct.sameOriginAsParent, false);
  assert.equal(direct.unsafe, false);

  const unsafe = deriveFrameTree({
    origin: HOST,
    children: [
      {
        srcdoc: "<script>alert(1)</script>",
        sandbox: "allow-scripts allow-same-origin",
      },
    ],
  }).children[0];

  assert.equal(unsafe.origin.base, HOST);
  assert.equal(unsafe.origin.effective, HOST);
  assert.equal(unsafe.sameOriginAsParent, true);
  assert.equal(unsafe.unsafe, true);
});

test("nested srcdoc inherits the parent frame's effective origin", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox: "allow-scripts allow-same-origin",
        children: [
          {
            srcdoc: "<script>alert(1)</script>",
            sandbox: "allow-scripts allow-same-origin",
          },
        ],
      },
    ],
  });

  const outer = tree.children[0];
  const inner = outer.children[0];

  assert.equal(outer.origin.effective, HOST);
  assert.equal(outer.unsafe, true);
  assert.equal(inner.origin.source, "srcdoc");
  assert.equal(inner.origin.base, HOST);
  assert.equal(inner.origin.effective, HOST);
  assert.equal(inner.sameOriginAsParent, true);
  assert.equal(inner.unsafe, true);

  // An intrinsically opaque parent keeps the srcdoc document opaque even with
  // allow-same-origin in the inherited flag set.
  const opaqueTree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: `data:text/html,<p>parent</p>`,
        sandbox: "allow-scripts allow-same-origin",
        children: [
          {
            srcdoc: "<p>child</p>",
            sandbox: "allow-scripts allow-same-origin",
          },
        ],
      },
    ],
  });
  const opaqueOuter = opaqueTree.children[0];
  const opaqueInner = opaqueOuter.children[0];

  assert.equal(opaqueOuter.origin.effective, "opaque");
  assert.equal(opaqueOuter.flags.sameOrigin, true);
  assert.equal(opaqueOuter.unsafe, false);
  assert.equal(opaqueInner.origin.base, "opaque");
  assert.equal(opaqueInner.origin.effective, "opaque");
  assert.equal(opaqueInner.flags.sameOrigin, true);
  assert.equal(opaqueInner.sameOriginAsParent, false);
  assert.equal(opaqueInner.unsafe, false);
});

test("popups inherit flags by default", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox: "allow-scripts allow-popups",
        children: [{ src: `${HOST}/child` }],
      },
    ],
  });
  const opener = tree.children[0];
  const child = opener.children[0];

  assert.equal(opener.popup.allowed, true);
  assert.equal(opener.popup.source, "inherited");
  assert.equal(opener.popup.escaped, false);
  assert.deepEqual(opener.popup.effectiveTokens, [
    "allow-popups",
    "allow-scripts",
  ]);
  assert.equal(opener.popup.flags.scripts, true);
  assert.equal(opener.popup.flags.popups, true);
  assert.equal(opener.popup.sandboxed, true);
  assert.equal(opener.popup.unsafe, false);

  // A child with no sandbox attribute inherits both scripts and popups.
  assert.equal(child.popup.allowed, true);
  assert.equal(child.popup.source, "inherited");
  assert.equal(child.popup.escaped, false);
  assert.equal(child.popup.escapeTokenExplicit, false);
  assert.deepEqual(child.popup.effectiveTokens, [
    "allow-popups",
    "allow-scripts",
  ]);
  assert.equal(child.popup.sandboxed, true);
});

test("the popup escape token must be explicit on the opening frame", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox:
          "allow-scripts allow-popups allow-popups-to-escape-sandbox",
        children: [
          { src: `${HOST}/child` },
          {
            src: `${HOST}/child-with-token`,
            sandbox:
              "allow-scripts allow-popups allow-popups-to-escape-sandbox",
          },
        ],
      },
    ],
  });
  const opener = tree.children[0];
  const inheritedChild = opener.children[0];
  const explicitChild = opener.children[1];

  assert.equal(opener.popupEscapeTokenExplicit, true);
  assert.equal(opener.popup.escapeTokenExplicit, true);
  assert.equal(opener.popup.escaped, true);
  assert.equal(opener.popup.source, "escaped");
  assert.equal(opener.popup.sandboxed, false);
  assert.equal(opener.popup.flags.scripts, true);
  assert.deepEqual(
    opener.popup.effectiveTokens,
    [...SANDBOX_CAPABILITY_TOKENS],
  );
  assert.equal(opener.popup.unsafe, true);
  assert.equal(opener.unsafe, true);
  assert.deepEqual(opener.unsafeReasons, [
    "popup-escape-bypasses-sandbox",
  ]);

  // The token is not inherited as a permission. The child has no attribute,
  // so its popups keep the inherited sandbox flags.
  assert.equal(inheritedChild.popupEscapeTokenExplicit, false);
  assert.equal(inheritedChild.popup.escapeTokenExplicit, false);
  assert.equal(inheritedChild.popup.allowed, true);
  assert.equal(inheritedChild.popup.escaped, false);
  assert.equal(inheritedChild.popup.source, "inherited");
  assert.equal(inheritedChild.popup.sandboxed, true);
  assert.equal(inheritedChild.unsafe, false);

  // Repeating the token on the child makes the popup escape again.
  assert.equal(explicitChild.popupEscapeTokenExplicit, true);
  assert.equal(explicitChild.popup.escaped, true);
  assert.equal(explicitChild.popup.source, "escaped");
  assert.equal(explicitChild.popup.sandboxed, false);
  assert.equal(explicitChild.popup.unsafe, true);
});

test("escape token without allow-popups cannot create a popup", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox:
          "allow-scripts allow-popups-to-escape-sandbox",
      },
    ],
  });
  const frame = tree.children[0];

  assert.equal(frame.popupEscapeTokenExplicit, true);
  assert.equal(frame.popup.allowed, false);
  assert.equal(frame.popup.source, "blocked");
  assert.equal(frame.popup.escaped, false);
  assert.deepEqual(frame.popup.effectiveTokens, []);
  assert.equal(frame.popup.unsafe, false);
  assert.equal(frame.unsafe, false);
});

test("empty sandbox inherits all of its restrictions", () => {
  const tree = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox: "",
        children: [
          { src: `${HOST}/inherit-empty` },
          {
            src: `${HOST}/try-restore`,
            sandbox: "allow-scripts",
          },
        ],
      },
    ],
  });
  const empty = tree.children[0];
  const inherits = empty.children[0];
  const attemptsRestore = empty.children[1];

  assert.equal(empty.sandboxAttributePresent, true);
  assert.equal(empty.sandboxActive, true);
  assert.deepEqual(empty.effectiveTokens, []);
  assert.equal(empty.origin.effective, "opaque");
  assert.equal(empty.popup.allowed, false);

  assert.equal(inherits.sandboxAttributePresent, false);
  assert.equal(inherits.sandboxActive, true);
  assert.deepEqual(inherits.effectiveTokens, []);
  assert.equal(inherits.flagResolutions.scripts.source, "inherited");

  assert.equal(attemptsRestore.sandboxAttributePresent, true);
  assert.deepEqual(attemptsRestore.effectiveTokens, []);
  assert.equal(
    attemptsRestore.flagResolutions.scripts.source,
    "blocked-ancestor",
  );
});

test("direct derivation accepts a parent token context", () => {
  const child = deriveSandbox(
    {
      src: HOST_FRAME,
      sandbox: "allow-scripts allow-same-origin",
    },
    {
      origin: HOST,
      effectiveTokens: ["allow-scripts"],
      sandboxActive: true,
    },
  );

  assert.equal(child.hasParent, true);
  assert.equal(child.sandboxActive, true);
  assert.deepEqual(child.effectiveTokens, ["allow-scripts"]);
  assert.equal(child.flags.sameOrigin, false);
  assert.equal(
    child.flagResolutions.sameOrigin.source,
    "blocked-ancestor",
  );
  assert.equal(child.origin.effective, "opaque");
  assert.equal(child.unsafe, false);
});

test("derivePopup can resolve a navigation target origin", () => {
  const frame = deriveFrameTree({
    origin: HOST,
    children: [
      {
        src: HOST_FRAME,
        sandbox:
          "allow-popups allow-popups-to-escape-sandbox",
      },
    ],
  }).children[0];

  assert.equal(frame.popup.sandboxed, false);
  assert.equal(
    derivePopup(frame).origin,
    "opaque",
  );
  assert.equal(
    derivePopup(frame, `${EVIL}/popup`).origin,
    "https://evil.example",
  );
});
