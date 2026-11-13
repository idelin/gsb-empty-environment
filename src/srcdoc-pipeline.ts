import {
  adjudicateSandboxFlags,
  requireSandboxApproval,
  type SandboxApproval,
  type SandboxFlagSelection,
  type SandboxFlagsInput,
  type SandboxVerdict,
} from "./sandbox-policy.ts";

export interface SrcdocVariant {
  readonly id: string;
  readonly flags: SandboxFlagSelection;
}

export interface SrcdocDocument {
  readonly id: string;
  readonly flags: SandboxFlagSelection;
  readonly sandboxAttribute: string;
  readonly origin: SandboxApproval["origin"];
  readonly executesScripts: boolean;
  readonly srcdoc: string;
}

export interface SrcdocAttempt {
  readonly id: string;
  readonly flags: SandboxFlagSelection;
  readonly verdict: SandboxVerdict;
  readonly document: SrcdocDocument | null;
}

export interface BuildSrcdocOptions {
  readonly id?: string;
  readonly html?: string;
}

export const SRCDOC_VARIANTS: readonly SrcdocVariant[] = [
  {
    id: "no-flags",
    flags: { allowScripts: false, allowSameOrigin: false },
  },
  {
    id: "scripts-only",
    flags: { allowScripts: true, allowSameOrigin: false },
  },
  {
    id: "same-origin-only",
    flags: { allowScripts: false, allowSameOrigin: true },
  },
  {
    id: "scripts-and-same-origin",
    flags: { allowScripts: true, allowSameOrigin: true },
  },
];

export const DEFAULT_SRCDOC_HTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>srcdoc sandbox probe</title>
</head>
<body>
  <p>srcdoc sandbox probe</p>
  <script>
    try {
      parent.postMessage({
        type: "srcdoc-probe",
        parentDocumentReadable: Boolean(parent.document),
      }, "*");
    } catch {
      parent.postMessage({
        type: "srcdoc-probe",
        parentDocumentReadable: false,
      }, "*");
    }
  </script>
</body>
</html>
`;

function buildDocument(
  id: string,
  approval: SandboxApproval,
  srcdoc: string,
): SrcdocDocument {
  if (approval.executesScripts && approval.sandboxAttribute.includes("allow-same-origin")) {
    throw new Error("invariant violated: script execution requires an opaque origin");
  }

  return {
    id,
    flags: approval.flags,
    sandboxAttribute: approval.sandboxAttribute,
    origin: approval.origin,
    executesScripts: approval.executesScripts,
    srcdoc,
  };
}

export function buildSrcdocDocument(
  flags: SandboxFlagsInput,
  options: BuildSrcdocOptions = {},
): SrcdocDocument {
  const approval = requireSandboxApproval(flags);
  const id = options.id ?? variantIdFor(approval.flags);
  return buildDocument(id, approval, options.html ?? DEFAULT_SRCDOC_HTML);
}

export function buildSrcdocMatrix(
  options: Pick<BuildSrcdocOptions, "html"> = {},
): readonly SrcdocAttempt[] {
  const srcdoc = options.html ?? DEFAULT_SRCDOC_HTML;

  return SRCDOC_VARIANTS.map((variant) => {
    const verdict = adjudicateSandboxFlags(variant.flags);
    return {
      id: variant.id,
      flags: variant.flags,
      verdict,
      document: verdict.allowed ? buildDocument(variant.id, verdict, srcdoc) : null,
    };
  });
}

export function buildSrcdocDocuments(
  options: Pick<BuildSrcdocOptions, "html"> = {},
): readonly SrcdocDocument[] {
  return buildSrcdocMatrix(options)
    .map((attempt) => attempt.document)
    .filter((document): document is SrcdocDocument => document !== null);
}

function escapeAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function renderSrcdocIframe(document: SrcdocDocument): string {
  const sandbox = escapeAttribute(document.sandboxAttribute);
  const srcdoc = escapeAttribute(document.srcdoc);
  return `<iframe sandbox="${sandbox}" srcdoc="${srcdoc}" referrerpolicy="no-referrer"></iframe>`;
}

function variantIdFor(flags: SandboxFlagSelection): string {
  if (flags.allowScripts && flags.allowSameOrigin) {
    return "scripts-and-same-origin";
  }
  if (flags.allowScripts) {
    return "scripts-only";
  }
  if (flags.allowSameOrigin) {
    return "same-origin-only";
  }
  return "no-flags";
}
