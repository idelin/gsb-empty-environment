# Referrer-policy portal component

This package is an importable TypeScript component for portals that serve email,
help-center, reset-password, authentication and account pages. It closes the
reset-token leak caused by a missing `Referrer-Policy` header and by copying
token-bearing URLs into outbound links.

## What it enforces

| Surface | Referrer-Policy | Notes |
| --- | --- | --- |
| Every response | `strict-origin-when-cross-origin` | Cross-origin requests receive the origin only; downgrades receive nothing. |
| Email, help, reset, auth, account paths | `no-referrer` | Also gets `Cache-Control: no-store`. |
| Any URL with a token-like query/fragment parameter | `no-referrer` | This is a security floor even for an otherwise public path. |
| Redirect and relay responses | `no-referrer` | The initiating URL is never forwarded. |

The component also:

- removes token-like parameters (`token`, `reset_token`, `accessToken`, `code`,
  `signature`, `api_key`, `email`, etc.) from share URLs and outbound links;
- blocks direct external links on sensitive pages by default, rewriting them
  through an opaque one-time relay instead;
- strips tokens and credentials from redirect destinations on the server;
- never copies the request's own query string into a `Location` header;
- emits `rel="noopener noreferrer"` and a per-anchor `referrerpolicy` for links.

## Usage

```ts
import { createReferrerPortal } from "./index.ts";

const portal = createReferrerPortal({
  baseUrl: "https://portal.example",
  relayPath: "/__referrer-relay",
  redirectPath: "/__referrer-redirect",
  // Configure this in production. Omit to allow any http(s) destination.
  allowedOrigins: ["https://docs.example", "https://*.partner.example"],
});

// Express/Connect: always writes the graded policy header and owns the
// relay/redirect routes.
app.use(portal.middleware);

// Link rendering: sensitive page external links become one-time relay links.
const decision = portal.resolveLink("https://docs.example/guide?token=abc", {
  pagePath: req.path,
});
const props = portal.anchorProps("https://docs.example/guide?token=abc", {
  pagePath: req.path,
});
// props.href contains no token; props.rel is "noopener noreferrer".

// Share URLs never carry tokens.
const share = portal.shareUrl("/reset?token=abc&lang=en");
// => "/reset?lang=en"
```

The pure helpers are available when a framework adapter is not needed:

```ts
import {
  buildReferrerPolicyHeaders,
  classifyOutboundLink,
  createRelay,
  createReferrerPolicyMiddleware,
  handleRedirectRequest,
  resolveReferrerPolicy,
  stripSensitiveParams,
} from "./index.ts";
```

## Server-side redirects

The redirect endpoint accepts `url`, `target`, `to`, `next`, `redirect`,
`redirect_uri`, `destination` or `dest`. It sanitizes the destination on the
server and rejects unsafe schemes and credentials. Safe query parameters
survive when no token is present; a token-bearing destination has its entire
query stripped:

```ts
const result = handleRedirectRequest(
  "/__referrer-redirect?url=https%3A%2F%2Fpartner.example%2Fcb%3Ftoken%3Dabc%26topic%3Dhelp",
  { mountPath: "/__referrer-redirect", externalMode: "relay", relay },
);

result.status;   // 302
result.location; // relay URL, never the token or destination
```

Set `stripAllQuery: true` when the redirect route must drop the destination's
entire query string. Use `allowedOrigins` plus `allowAnyExternal: false` to
close an open redirect surface.

## One-time relay

```ts
const relay = createRelay({ baseUrl: "https://portal.example" });
const link = relay.createLink("https://docs.example/guide?token=abc");

relay.handle(link.url); // 302 -> https://docs.example/guide
relay.handle(link.url); // 410 Gone (token already consumed)
```

`InMemoryRelayStore` is intentionally small and single-process. In a
multi-instance deployment, pass a shared `RelayStore` implementation (for
example Redis) to `createRelay({ store })`; the one-time `consume` contract is
the only requirement.

## Tests

```sh
node --experimental-strip-types --test
```

The suite covers the five incident scenarios:

1. External link leak: sensitive pages never render a direct external link and
   relay URLs contain no reset token.
2. Missing policy: every response receives a strict header, replacing a missing
   or permissive one.
3. Cross-origin redirect: `Location` is sanitized and carries no request query
   or token.
4. Token relay: relay links are opaque, one-time, expired/replayed tokens are
   rejected, and the final destination contains no sensitive parameters.
5. Legitimate external links: safe query parameters survive, tokens are
   removed, and the third party receives neither a token nor a full referrer.
