import {
  buildSrcdocMatrix,
  renderSrcdocIframe,
} from "../index.ts";

const rows = buildSrcdocMatrix().map((attempt) => ({
  id: attempt.id,
  flags: attempt.flags,
  verdict: attempt.verdict.allowed
    ? {
        allowed: true,
        sandboxAttribute: attempt.verdict.sandboxAttribute,
        origin: attempt.verdict.origin,
        executesScripts: attempt.verdict.executesScripts,
      }
    : {
        allowed: false,
        reason: attempt.verdict.reason,
        message: attempt.verdict.message,
      },
  srcdocBuilt: attempt.document !== null,
  iframe: attempt.document === null ? null : renderSrcdocIframe(attempt.document),
}));

console.log(JSON.stringify(rows, null, 2));
