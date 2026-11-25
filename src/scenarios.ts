import { freezeDocument, SCHEMA } from "./core.ts";
import { issueCertificate, verifyMigration } from "./certificate.ts";
import {
  buildStackSummaryFromSession,
  experimentalContinueWithContinuations,
  migrateAndContinue,
  receiptDraftFromSession,
  renderToCompletion,
  runToBoundary,
  type RenderSession,
} from "./host.ts";
import { HmacReceiptAuthenticator } from "./receipts.ts";
import { stackSummaryHash } from "./stack.ts";
import { Contexts } from "./types.ts";
import type {
  EncoderSet,
  HostDataProvider,
  MigrationCertificate,
  StackSummary,
  TemplateDocument,
  TemplateFragment,
} from "./types.ts";

export const RECEIPT_SECRET = "unit-test-receipt-secret-0123456789";
export const CERT_SECRET = "unit-test-cert-secret-0123456789";

export const Labels = {
  badgeTitle: "source:profile.title#user",
  badgeName: "source:profile.name#user",
  commentBody: "source:comment.body#user",
  avatarReturnTo: "source:comment.avatar-return-to#user",
  editedNote: "source:comment.edited-note#system",
} as const;

export function createEncoderSet(version: "old" | "new"): EncoderSet {
  if (version === "old") {
    return {
      html: {
        schema: SCHEMA.encoderTable,
        id: "html",
        version: "1.0.0",
        initialStateId: "text",
        states: {
          text: {
            contextId: Contexts.HtmlText,
            escape: { "&": "&amp;", "<": "&lt;", ">": "&gt;" },
          },
          attr: {
            contextId: Contexts.HtmlDoubleAttribute,
            escape: {
              "&": "&amp;",
              "<": "&lt;",
              ">": "&gt;",
              '"': "&quot;",
              "'": "&#39;",
            },
          },
        },
      },
      url: {
        schema: SCHEMA.encoderTable,
        id: "url",
        version: "1.0.0",
        initialStateId: "query",
        states: {
          query: {
            contextId: Contexts.UrlQueryComponent,
            escape: {
              "&": "%26",
              "=": "%3D",
              "#": "%23",
              "+": "%2B",
              "%": "%25",
              " ": "%20",
              '"': "%22",
            },
          },
        },
      },
      raw: {
        schema: SCHEMA.encoderTable,
        id: "raw",
        version: "1.0.0",
        initialStateId: "raw",
        states: { raw: { contextId: Contexts.Raw, escape: {} } },
      },
    };
  }

  return {
    html: {
      schema: SCHEMA.encoderTable,
      id: "html",
      version: "2.0.0-renamed-states",
      initialStateId: "body",
      states: {
        body: {
          contextId: Contexts.HtmlText,
          escape: { "&": "&amp;", "<": "&lt;", ">": "&gt;" },
        },
        doubleQuotedAttribute: {
          contextId: Contexts.HtmlDoubleAttribute,
          escape: {
            "&": "&amp;",
            "<": "&lt;",
            ">": "&gt;",
            '"': "&quot;",
            "'": "&#39;",
          },
        },
      },
    },
    url: {
      schema: SCHEMA.encoderTable,
      id: "url",
      version: "2.0.0-renamed-states",
      initialStateId: "queryComponent",
      states: {
        queryComponent: {
          contextId: Contexts.UrlQueryComponent,
          escape: {
            "&": "%26",
            "=": "%3D",
            "#": "%23",
            "+": "%2B",
            "%": "%25",
            " ": "%20",
            '"': "%22",
          },
        },
      },
    },
    raw: {
      schema: SCHEMA.encoderTable,
      id: "raw",
      version: "2.0.0",
      initialStateId: "raw",
      states: { raw: { contextId: Contexts.Raw, escape: {} } },
    },
  };
}

function badgeFragment(version: "old" | "new"): TemplateFragment {
  const attrState = version === "old" ? "attr" : "doubleQuotedAttribute";
  const textState = version === "old" ? "text" : "body";
  return {
    id: "userBadge",
    nodes: [
      { id: "badge-open", kind: "literal", value: '<span class="badge" title="', contextAfter: Contexts.HtmlDoubleAttribute },
      {
        id: "badge-title",
        kind: "field",
        bindingId: "badge.title",
        sourceLabel: Labels.badgeTitle,
        encoderId: "html",
        stateId: attrState,
        contextId: Contexts.HtmlDoubleAttribute,
        authorizedContextIds: [Contexts.HtmlDoubleAttribute],
      },
      {
        id: "badge-title-close",
        kind: "literal",
        value: '" data-shared-fragment="userBadge">',
        contextAfter: Contexts.HtmlText,
      },
      {
        id: "badge-name",
        kind: "field",
        bindingId: "badge.name",
        sourceLabel: Labels.badgeName,
        encoderId: "html",
        stateId: textState,
        contextId: Contexts.HtmlText,
        authorizedContextIds: [Contexts.HtmlText],
      },
      { id: "badge-close", kind: "literal", value: "</span>" },
      { id: "badge-return", kind: "return" },
    ],
  };
}

export function createThreadTemplate(version: "old" | "new"): TemplateDocument {
  const htmlTextState = version === "old" ? "text" : "body";
  const htmlAttrState = version === "old" ? "attr" : "doubleQuotedAttribute";
  const urlState = version === "old" ? "query" : "queryComponent";
  const badge = badgeFragment(version);

  return {
    schema: SCHEMA.template,
    id: "thread-template",
    version: version === "old" ? "2026.11.1-old" : "2026.11.2-new",
    entryFragmentId: "page",
    fragments: {
      userBadge: badge,
      page: {
        id: "page",
        nodes: [
          { id: "page-doctype", kind: "literal", value: "<!doctype html><html><body>" },
          { id: "page-header-open", kind: "literal", value: "<header><h1>Pair-wise thread</h1>" },
          {
            id: "page-call-badge",
            kind: "call",
            targetFragmentId: "userBadge",
            returnPointId: "page-rp-header-badge",
          },
          {
            id: "page-rp-header-badge",
            kind: "literal",
            value: "</header><section class=\"thread\">",
          },
          {
            id: "page-call-comment",
            kind: "call",
            targetFragmentId: "commentItem",
            returnPointId: "page-rp-comments",
          },
          {
            id: "page-rp-comments",
            kind: "literal",
            value: "</section><footer>All obligations retained</footer></body></html>",
          },
          { id: "page-return", kind: "return" },
        ],
      },
      commentItem: {
        id: "commentItem",
        nodes: [
          { id: "ci-open", kind: "literal", value: '<section class="comment">' },
          {
            id: "ci-call-badge",
            kind: "call",
            targetFragmentId: "userBadge",
            returnPointId: "ci-rp-badge",
          },
          { id: "ci-rp-badge", kind: "literal", value: "<p>" },
          {
            id: "ci-body",
            kind: "field",
            bindingId: "comment.body",
            sourceLabel: Labels.commentBody,
            encoderId: "html",
            stateId: htmlTextState,
            contextId: Contexts.HtmlText,
            authorizedContextIds: [Contexts.HtmlText],
          },
          { id: "ci-body-close", kind: "literal", value: "</p>" },
          {
            id: "ci-call-reply",
            kind: "call",
            targetFragmentId: "commentItem",
            returnPointId: "ci-after-replies",
            conditionBindingId: "comment.hasReply",
          },
          {
            id: "ci-after-replies",
            kind: "literal",
            value: '<footer><img src="/avatar?next=',
            contextAfter: Contexts.UrlQueryComponent,
          },
          {
            id: "ci-return-to",
            kind: "field",
            bindingId: "avatar.returnTo",
            sourceLabel: Labels.avatarReturnTo,
            encoderId: "url",
            stateId: urlState,
            contextId: Contexts.UrlQueryComponent,
            authorizedContextIds: [Contexts.UrlQueryComponent, Contexts.HtmlDoubleAttribute],
          },
          {
            id: "ci-avatar-close",
            kind: "literal",
            value: '" alt="avatar"></footer>',
            contextAfter: Contexts.HtmlText,
          },
          {
            id: "ci-boundary",
            kind: "boundary",
            boundaryId: "thread-boundary",
            alignmentKey: "thread-prefix-aligned-v1",
          },
          {
            id: "ci-edited",
            kind: "field",
            bindingId: "comment.edited",
            sourceLabel: Labels.editedNote,
            encoderId: "html",
            stateId: htmlTextState,
            contextId: Contexts.HtmlText,
            authorizedContextIds: [Contexts.HtmlText],
          },
          { id: "ci-close", kind: "literal", value: "</section>" },
          { id: "ci-return", kind: "return" },
        ],
      },
      vulnerableTail: {
        id: "vulnerableTail",
        nodes: [
          { id: "vulnerable-start", kind: "literal", value: "EXPERIMENT-RAW:" },
          {
            id: "vulnerable-body",
            kind: "field",
            bindingId: "comment.body",
            sourceLabel: Labels.commentBody,
            encoderId: "raw",
            stateId: "raw",
            contextId: Contexts.Raw,
            authorizedContextIds: [Contexts.Raw],
          },
          { id: "vulnerable-return", kind: "return" },
        ],
      },
    },
  };
}

const fixedThreadData: Record<string, { value: string; sourceLabel: string }> = {
  "badge.title": { value: `O'Reilly & "friends"`, sourceLabel: Labels.badgeTitle },
  "badge.name": { value: "A <b> & \"Co\"", sourceLabel: Labels.badgeName },
  "comment.body": {
    value: `<img src=x onerror="alert('deep')"> & "quote"`,
    sourceLabel: Labels.commentBody,
  },
  "avatar.returnTo": { value: `a&b=c#d e%f"<>`, sourceLabel: Labels.avatarReturnTo },
  "comment.edited": { value: "<edited> & safe", sourceLabel: Labels.editedNote },
};

export function createThreadData(commentFrames: number): HostDataProvider {
  return (request) => {
    if (request.bindingId === "comment.hasReply") {
      return {
        value: request.depth < commentFrames ? "yes" : "no",
        sourceLabel: "source:control/recursion-depth",
      };
    }
    return fixedThreadData[request.bindingId];
  };
}

export interface ThreadHarness {
  authenticator: HmacReceiptAuthenticator;
  oldTemplate: ReturnType<typeof freezeDocument<TemplateDocument>>;
  newTemplate: ReturnType<typeof freezeDocument<TemplateDocument>>;
  oldEncoders: ReturnType<typeof freezeDocument<EncoderSet>>;
  newEncoders: ReturnType<typeof freezeDocument<EncoderSet>>;
  data: HostDataProvider;
  prefixSession: RenderSession;
  summary: StackSummary;
  receipt: ReturnType<HmacReceiptAuthenticator["authenticate"]>;
  certificate: MigrationCertificate;
  verification: ReturnType<typeof verifyMigration>;
  safeContinuations: MigrationCertificate["continuations"];
  encoderBindings: MigrationCertificate["encoderBindings"];
  freshPrefixSession: () => RenderSession;
  verifyCertificate: (certificate: MigrationCertificate, summaryOverride?: StackSummary) => ReturnType<typeof verifyMigration>;
}

export function createThreadHarness(commentFrames = 3): ThreadHarness {
  const authenticator = new HmacReceiptAuthenticator(RECEIPT_SECRET);
  const oldTemplate = freezeDocument(createThreadTemplate("old"));
  const newTemplate = freezeDocument(createThreadTemplate("new"));
  const oldEncoders = freezeDocument(createEncoderSet("old"));
  const newEncoders = freezeDocument(createEncoderSet("new"));
  const data = createThreadData(commentFrames);

  const freshPrefixSession = () =>
    runToBoundary({
      template: oldTemplate.value,
      encoders: oldEncoders.value,
      data,
      boundaryId: "thread-boundary",
    });

  const prefixSession = freshPrefixSession();
  const summary = buildThreadSummary(prefixSession, oldTemplate.sha256);
  const receiptDraft = receiptDraftFromSession(prefixSession, {
    receiptId: "receipt-thread-001",
    oldTemplateHash: oldTemplate.sha256,
    stackSummaryHash: stackSummaryHash(summary),
    issuedAt: "2026-11-25T00:00:00Z",
    nonce: "nonce-thread",
  });
  const receipt = authenticator.authenticate(receiptDraft);

  const safeContinuations: MigrationCertificate["continuations"] = {
    "page:page-rp-comments": {
      newFragmentId: "page",
      newResumeNodeId: "page-rp-comments",
      contextId: Contexts.HtmlText,
    },
    "commentItem:ci-after-replies": {
      newFragmentId: "commentItem",
      newResumeNodeId: "ci-after-replies",
      contextId: Contexts.HtmlText,
      encoderRef: { encoderId: "html", stateId: "body" },
    },
    "commentItem:ci-boundary": {
      newFragmentId: "commentItem",
      newResumeNodeId: "ci-boundary",
      contextId: Contexts.HtmlText,
    },
  };

  const encoderBindings: MigrationCertificate["encoderBindings"] = [
    { oldEncoderId: "html", oldStateId: "text", newEncoderId: "html", newStateId: "body" },
    { oldEncoderId: "html", oldStateId: "attr", newEncoderId: "html", newStateId: "doubleQuotedAttribute" },
    { oldEncoderId: "url", oldStateId: "query", newEncoderId: "url", newStateId: "queryComponent" },
  ];

  const certificate = issueCertificate({
    certificateId: "cert-thread-safe-001",
    boundaryId: "thread-boundary",
    alignmentKey: "thread-prefix-aligned-v1",
    oldTemplate,
    newTemplate,
    oldEncoders,
    newEncoders,
    stackSummary: summary,
    receipt,
    continuations: safeContinuations,
    encoderBindings,
  });

  const verifyCertificate = (
    candidate: MigrationCertificate,
    summaryOverride: StackSummary = summary,
  ) =>
    verifyMigration({
      certificate: candidate,
      oldTemplate,
      newTemplate,
      oldEncoders,
      newEncoders,
      stackSummary: summaryOverride,
      receipt,
      prefix: prefixSession.output,
      authenticator,
    });

  const verification = verifyCertificate(certificate);

  return {
    authenticator,
    oldTemplate,
    newTemplate,
    oldEncoders,
    newEncoders,
    data,
    prefixSession,
    summary,
    receipt,
    certificate,
    verification,
    safeContinuations,
    encoderBindings,
    freshPrefixSession,
    verifyCertificate,
  };
}

function buildThreadSummary(session: RenderSession, oldTemplateHash: string): StackSummary {
  return buildStackSummaryFromSession(session, {
    summaryId: "stack-thread-recursive-v1",
    oldTemplateHash,
    obligationByKind: {
      "page:page-rp-comments": {
        requiredCloseTokens: ["</section>", "</body>", "</html>"],
      },
      "commentItem:ci-after-replies": {
        requiredCloseTokens: ['" alt="avatar"></footer>', "</section>"],
      },
      "commentItem:ci-boundary": {
        requiredCloseTokens: ["</section>"],
      },
    },
  });
}

export function runSafeThreadMigration(harness: ThreadHarness): RenderSession {
  const session = harness.freshPrefixSession();
  return migrateAndContinue({
    session,
    verification: harness.verification,
    newTemplate: harness.newTemplate.value,
    encoders: harness.newEncoders.value,
    data: harness.data,
  });
}

export function runOriginalThreadToCompletion(harness: ThreadHarness, commentFrames = 2): RenderSession {
  return renderToCompletion({
    template: harness.oldTemplate.value,
    encoders: harness.oldEncoders.value,
    data: createThreadData(commentFrames),
  });
}

export function createDeepBadCertificate(harness: ThreadHarness): MigrationCertificate {
  const bad: MigrationCertificate = structuredClone(harness.certificate);
  bad.certificateId = "cert-thread-deep-raw-attack";
  bad.continuations["commentItem:ci-after-replies"] = {
    newFragmentId: "vulnerableTail",
    newResumeNodeId: "vulnerable-start",
    contextId: Contexts.Raw,
    encoderRef: { encoderId: "raw", stateId: "raw" },
  };
  bad.encoderBindings.push({
    oldEncoderId: "html",
    oldStateId: "text",
    newEncoderId: "raw",
    newStateId: "raw",
    mergeClass: "rename-state-to-bypass-html-context",
  });
  bad.universalProof.relevantFrameIds = [...bad.universalProof.relevantFrameIds];
  return bad;
}

export function runForcedDeepBadMigration(harness: ThreadHarness): RenderSession {
  const session = harness.freshPrefixSession();
  const badCert = createDeepBadCertificate(harness);
  return experimentalContinueWithContinuations({
    session,
    template: harness.newTemplate.value,
    encoders: harness.newEncoders.value,
    data: harness.data,
    continuations: badCert.continuations,
  });
}

const MERGE_RP_A = "branchA:branch-a-rp";
const MERGE_RP_B = "branchB:branch-b-rp";
const MERGE_PROBE_TOP = "sharedProbe:probe-boundary";

const sharedProbeFragment: TemplateDocument["fragments"][string] = {
  id: "sharedProbe",
  nodes: [
    { id: "probe-open", kind: "literal", value: "<em>shared probe</em>" },
    {
      id: "probe-boundary",
      kind: "boundary",
      boundaryId: "merge-boundary",
      alignmentKey: "merge-aligned-v1",
    },
    {
      id: "probe-field",
      kind: "field",
      bindingId: "merge.field",
      sourceLabel: "source:merge.field#user",
      encoderId: "html",
      contextId: Contexts.HtmlText,
      authorizedContextIds: [Contexts.HtmlText],
    },
    { id: "probe-return", kind: "return" },
  ],
};

function createSharedProbeFragment() {
  return sharedProbeFragment;
}

export function createMergeTemplate(version: "old" | "new"): TemplateDocument {
  const probe = createSharedProbeFragment();
  return {
    schema: SCHEMA.template,
    id: "merge-return-template",
    version: version === "old" ? "1.0.0" : "2.0.0-merged-return",
    entryFragmentId: "branchA",
    fragments: {
      sharedProbe: probe,
      branchA: {
        id: "branchA",
        nodes: [
          { id: "branch-a-open", kind: "literal", value: '<section data-branch="A">' },
          {
            id: "branch-a-call-probe",
            kind: "call",
            targetFragmentId: "sharedProbe",
            returnPointId: "branch-a-rp",
          },
          { id: "branch-a-rp", kind: "literal", value: "</section>" },
          { id: "branch-a-return", kind: "return" },
        ],
      },
      branchB: {
        id: "branchB",
        nodes: [
          { id: "branch-b-open", kind: "literal", value: '<section data-branch="B">' },
          {
            id: "branch-b-call-probe",
            kind: "call",
            targetFragmentId: "sharedProbe",
            returnPointId: "branch-b-rp",
          },
          { id: "branch-b-rp", kind: "literal", value: "</section>" },
          { id: "branch-b-return", kind: "return" },
        ],
      },
      ...(version === "new"
        ? {
            mergedReturn: {
              id: "mergedReturn",
              nodes: [
                { id: "merged-rp", kind: "literal" as const, value: "</section>" },
                { id: "merged-return", kind: "return" as const },
              ],
            },
          }
        : {}),
    },
  };
}

export interface MergeHarness {
  authenticator: HmacReceiptAuthenticator;
  oldTemplate: ReturnType<typeof freezeDocument<TemplateDocument>>;
  newTemplate: ReturnType<typeof freezeDocument<TemplateDocument>>;
  oldEncoders: ReturnType<typeof freezeDocument<EncoderSet>>;
  newEncoders: ReturnType<typeof freezeDocument<EncoderSet>>;
  data: HostDataProvider;
  sessionA: RenderSession;
  summary: StackSummary;
  receipt: ReturnType<HmacReceiptAuthenticator["authenticate"]>;
  certificate: MigrationCertificate;
  verification: ReturnType<typeof verifyMigration>;
  migratedA: RenderSession;
  originalA: RenderSession;
  originalB: RenderSession;
}

export function createMergeHarness(): MergeHarness {
  const authenticator = new HmacReceiptAuthenticator(RECEIPT_SECRET, "merge-key");
  const oldTemplate = freezeDocument(createMergeTemplate("old"));
  const newTemplate = freezeDocument(createMergeTemplate("new"));
  const oldEncoders = freezeDocument(createEncoderSet("old"));
  const newEncoders = freezeDocument(createEncoderSet("new"));
  const data: HostDataProvider = {
    "merge.field": { value: `<merge & "x">`, sourceLabel: "source:merge.field#user" },
  };

  const sessionA = runToBoundary({
    template: oldTemplate.value,
    encoders: oldEncoders.value,
    data,
    entryFragmentId: "branchA",
    boundaryId: "merge-boundary",
  });

  const summary = buildStackSummaryFromSession(sessionA, {
    summaryId: "stack-merge-two-return-points",
    oldTemplateHash: oldTemplate.sha256,
    obligationByKind: {
      [MERGE_RP_A]: { requiredCloseTokens: ["</section>"] },
      [MERGE_PROBE_TOP]: { requiredCloseTokens: [] },
    },
  });
  summary.rootFrameIds = [MERGE_RP_A, MERGE_RP_B];
  summary.frames[MERGE_RP_B] = {
    id: MERGE_RP_B,
    fragmentId: "branchB",
    resumeNodeId: "branch-b-rp",
    returnPointNodeId: "branch-b-rp",
    contextId: Contexts.HtmlText,
    activeBindingIds: [],
    requiredCloseTokens: ["</section>"],
  };
  summary.edges.push({
    callerFrameId: MERGE_RP_B,
    callSiteNodeId: "branch-b-call-probe",
    calleeFrameId: MERGE_PROBE_TOP,
  });

  const receipt = authenticator.authenticate(
    receiptDraftFromSession(sessionA, {
      receiptId: "receipt-merge-a",
      oldTemplateHash: oldTemplate.sha256,
      stackSummaryHash: stackSummaryHash(summary),
    }),
  );

  const continuations: MigrationCertificate["continuations"] = {
    [MERGE_RP_A]: {
      newFragmentId: "mergedReturn",
      newResumeNodeId: "merged-rp",
      contextId: Contexts.HtmlText,
      mergeGroupId: "section-close-merge",
    },
    [MERGE_RP_B]: {
      newFragmentId: "mergedReturn",
      newResumeNodeId: "merged-rp",
      contextId: Contexts.HtmlText,
      mergeGroupId: "section-close-merge",
    },
    [MERGE_PROBE_TOP]: {
      newFragmentId: "sharedProbe",
      newResumeNodeId: "probe-boundary",
      contextId: Contexts.HtmlText,
    },
  };

  const certificate = issueCertificate({
    certificateId: "cert-merge-safe",
    boundaryId: "merge-boundary",
    alignmentKey: "merge-aligned-v1",
    oldTemplate,
    newTemplate,
    oldEncoders,
    newEncoders,
    stackSummary: summary,
    receipt,
    continuations,
    encoderBindings: [
      { oldEncoderId: "html", oldStateId: "text", newEncoderId: "html", newStateId: "body" },
    ],
    returnPointMerges: [
      {
        mergeGroupId: "section-close-merge",
        oldReturnPointNodeIds: ["branch-a-rp", "branch-b-rp"],
        newReturnPointNodeId: "merged-rp",
      },
    ],
  });

  const verification = verifyMigration({
    certificate,
    oldTemplate,
    newTemplate,
    oldEncoders,
    newEncoders,
    stackSummary: summary,
    receipt,
    prefix: sessionA.output,
    authenticator,
  });

  const migratedA = migrateAndContinue({
    session: structuredClone(sessionA),
    verification,
    newTemplate: newTemplate.value,
    encoders: newEncoders.value,
    data,
  });

  const originalA = renderToCompletion({
    template: oldTemplate.value,
    encoders: oldEncoders.value,
    data,
    entryFragmentId: "branchA",
  });
  const originalB = renderToCompletion({
    template: oldTemplate.value,
    encoders: oldEncoders.value,
    data,
    entryFragmentId: "branchB",
  });

  return {
    authenticator,
    oldTemplate,
    newTemplate,
    oldEncoders,
    newEncoders,
    data,
    sessionA,
    summary,
    receipt,
    certificate,
    verification,
    migratedA,
    originalA,
    originalB,
  };
}
