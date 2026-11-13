import type { BrowsingTopic, TopicsHost } from './topics-host.ts';

export interface FrameContext {
  /** Origin of the document executing the call. */
  documentOrigin: string;
  /** Registrable site of the top-level page. */
  topLevelSite: string;
  /** False when the document is an embedded iframe. */
  isTopLevel: boolean;
  /** Origin of the embedding frame; null for the top-level document. */
  parentOrigin: string | null;
  /**
   * Resolved browsing-topics Permissions Policy: true only when an
   * allow="browsing-topics" header grants this document access.
   */
  browsingTopicsAllowed: boolean;
}

export interface BrowsingTopicsDocument {
  browsingTopics: () => Promise<BrowsingTopic[]>;
}

function isCrossOrigin(context: FrameContext): boolean {
  if (context.isTopLevel || context.parentOrigin === null) {
    return false;
  }
  return new URL(context.documentOrigin).origin !== new URL(context.parentOrigin).origin;
}

/**
 * A cross-origin iframe is denied unless the page explicitly opts in via the
 * browsing-topics Permissions Policy. Same-origin documents inherit access
 * through the allowlist, matching the browser's default policy behavior.
 */
export function resolveTopicsPolicy(context: FrameContext): boolean {
  if (isCrossOrigin(context)) {
    return context.browsingTopicsAllowed;
  }
  return true;
}

/**
 * Installs document.browsingTopics() backed by a TopicsHost. The frame
 * context is supplied by the embedding/browser layer; host storage itself is
 * never reachable from page scripts.
 */
export function installBrowsingTopics(
  documentTarget: Partial<BrowsingTopicsDocument>,
  host: TopicsHost,
  frameContext: FrameContext,
  callerOrigin: string,
): BrowsingTopicsDocument {
  const documentWithApi = documentTarget as BrowsingTopicsDocument;
  documentWithApi.browsingTopics = () =>
    host.fetchTopics({
      callerOrigin,
      topLevelSite: frameContext.topLevelSite,
      topicsPolicyAllowed: resolveTopicsPolicy(frameContext),
    });
  return documentWithApi;
}
