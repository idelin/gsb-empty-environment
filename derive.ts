import {
  CAPABILITY_KEY_TO_TOKEN,
  CAPABILITY_TOKEN_TO_KEY,
  POPUP_ESCAPE_TOKEN,
  SANDBOX_CAPABILITY_KEYS,
  SANDBOX_CAPABILITY_TOKENS,
  hasPopupEscapeToken,
  parseSandboxAttribute,
} from "./tokens.ts";
import {
  OPAQUE_ORIGIN,
  isSameOrigin,
  normalizeOrigin,
  resolveFrameOrigin,
} from "./origin.ts";
import type {
  FrameInput,
  FrameTreeDerivation,
  OriginResolution,
  PopupResolution,
  RootFrameInput,
  SandboxCapabilityToken,
  SandboxDerivation,
  SandboxFlagResolutions,
  SandboxFlagSet,
  SandboxParentContext,
  UnsafeReason,
} from "./types.ts";

interface ParentState {
  origin: string;
  flags: SandboxFlagSet;
  resolutions: SandboxFlagResolutions;
  path: string;
  sandboxActive: boolean;
}

function emptyFlags(): SandboxFlagSet {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => [key, false]),
  ) as unknown as SandboxFlagSet;
}

function allFlags(): SandboxFlagSet {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => [key, true]),
  ) as unknown as SandboxFlagSet;
}

function flagsFromResolutions(
  resolutions: SandboxFlagResolutions,
): SandboxFlagSet {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => [key, resolutions[key].allowed]),
  ) as unknown as SandboxFlagSet;
}

function rootResolutions(path: string): SandboxFlagResolutions {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => [
      key,
      { allowed: true, source: "root", declaredAt: path },
    ]),
  ) as unknown as SandboxFlagResolutions;
}

function inheritedResolutions(
  parent: ParentState,
): SandboxFlagResolutions {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => {
      const previous = parent.resolutions[key];
      return [
        key,
        {
          allowed: previous.allowed,
          source: "inherited",
          declaredAt: previous.declaredAt,
        },
      ];
    }),
  ) as unknown as SandboxFlagResolutions;
}

function declaredResolutions(
  declaredTokens: readonly SandboxCapabilityToken[],
  parent: ParentState | null,
  path: string,
): SandboxFlagResolutions {
  return Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => {
      const token = CAPABILITY_KEY_TO_TOKEN[key];
      const declaredHere = declaredTokens.includes(token);
      const parentAllowed = parent === null
        ? true
        : parent.flags[key];

      if (declaredHere && parentAllowed) {
        return [
          key,
          { allowed: true, source: "declared", declaredAt: path },
        ];
      }
      if (declaredHere && !parentAllowed) {
        return [
          key,
          {
            allowed: false,
            source: "blocked-ancestor",
            declaredAt: null,
          },
        ];
      }
      return [
        key,
        { allowed: false, source: "omitted", declaredAt: null },
      ];
    }),
  ) as unknown as SandboxFlagResolutions;
}

function effectiveTokensFromFlags(
  flags: SandboxFlagSet,
): SandboxCapabilityToken[] {
  return SANDBOX_CAPABILITY_TOKENS.filter(
    (token) => flags[CAPABILITY_TOKEN_TO_KEY[token]],
  );
}

function originForNode(
  frame: FrameInput,
  parent: ParentState | null,
  rootOrigin: string | undefined,
): OriginResolution {
  let source: OriginResolution["source"];
  let base: string;

  if (rootOrigin !== undefined) {
    source = "root";
    base = normalizeOrigin(rootOrigin, "root origin");
  } else if (frame.srcdoc !== undefined) {
    source = "srcdoc";
    base = parent?.origin ?? OPAQUE_ORIGIN;
  } else if (frame.src !== undefined && frame.src.trim() !== "") {
    const trimmed = frame.src.trim();
    if (trimmed === "about:blank" || trimmed === "about:srcdoc") {
      source = "about-blank";
      base = parent?.origin ?? OPAQUE_ORIGIN;
    } else {
      source = "url";
      base = resolveFrameOrigin(
        trimmed,
        parent?.origin ?? OPAQUE_ORIGIN,
      );
    }
  } else {
    source = "about-blank";
    base = parent?.origin ?? OPAQUE_ORIGIN;
  }

  const keepsOrigin = base !== OPAQUE_ORIGIN;
  return {
    source,
    base,
    effective: keepsOrigin ? base : OPAQUE_ORIGIN,
    forcedOpaque: false,
  };
}

function popupResolution(
  flags: SandboxFlagSet,
  effectiveTokens: SandboxCapabilityToken[],
  sandboxActive: boolean,
  escapeTokenExplicit: boolean,
  origin: string,
): PopupResolution {
  const allowed = flags.popups;
  const escaped = allowed && escapeTokenExplicit;
  const openerCanRunScripts = flags.scripts;

  if (!allowed) {
    return {
      allowed: false,
      source: "blocked",
      escapeTokenExplicit,
      escaped: false,
      effectiveTokens: [],
      flags: emptyFlags(),
      origin,
      sandboxed: true,
      unsafe: false,
    };
  }

  if (escaped) {
    return {
      allowed: true,
      source: "escaped",
      escapeTokenExplicit,
      escaped: true,
      effectiveTokens: [...SANDBOX_CAPABILITY_TOKENS],
      flags: allFlags(),
      origin,
      sandboxed: false,
      unsafe: openerCanRunScripts,
    };
  }

  return {
    allowed: true,
    source: "inherited",
    escapeTokenExplicit,
    escaped: false,
    effectiveTokens: [...effectiveTokens],
    flags: { ...flags },
    origin,
    sandboxed: sandboxActive,
    unsafe: false,
  };
}

function deriveNode(
  frame: FrameInput,
  parent: ParentState | null,
  path: string,
  rootOrigin?: string,
): SandboxDerivation {
  const parsed = parseSandboxAttribute(frame.sandbox);
  const sandboxActive =
    parsed.present || (parent !== null && parent.sandboxActive);

  let resolutions: SandboxFlagResolutions;
  if (parent === null && !parsed.present) {
    resolutions = rootResolutions(path);
  } else if (parent !== null && !parsed.present) {
    resolutions = inheritedResolutions(parent);
  } else {
    resolutions = declaredResolutions(parsed.tokens, parent, path);
  }

  const flags = flagsFromResolutions(resolutions);
  const effectiveTokens = effectiveTokensFromFlags(flags);
  const popupEscapeTokenExplicit = hasPopupEscapeToken(parsed);

  const origin = originForNode(frame, parent, rootOrigin);
  const keepsOrigin = flags.sameOrigin;
  const effectiveOrigin = keepsOrigin ? origin.base : OPAQUE_ORIGIN;
  const forcedOpaque =
    origin.base !== OPAQUE_ORIGIN && !keepsOrigin;
  origin.effective = effectiveOrigin;
  origin.forcedOpaque = forcedOpaque;

  const sameOriginAsParent =
    parent !== null &&
    isSameOrigin(origin.effective, parent.origin);

  const popup = popupResolution(
    flags,
    effectiveTokens,
    sandboxActive,
    popupEscapeTokenExplicit,
    origin.effective,
  );

  const unsafeReasons: UnsafeReason[] = [];
  if (
    sandboxActive &&
    sameOriginAsParent &&
    flags.scripts &&
    flags.sameOrigin
  ) {
    unsafeReasons.push("same-origin-scripts-can-remove-sandbox");
  }
  if (popup.unsafe) {
    unsafeReasons.push("popup-escape-bypasses-sandbox");
  }

  return {
    path,
    name: frame.name ?? null,
    hasParent: parent !== null,
    sandboxAttributePresent: parsed.present,
    sandboxActive,
    declaredTokens: [...parsed.tokens, ...parsed.modifiers],
    modifiers: [...parsed.modifiers],
    unknownTokens: [...parsed.unknownTokens],
    effectiveTokens,
    flags,
    flagResolutions: resolutions,
    popupEscapeTokenExplicit,
    origin,
    sameOriginAsParent,
    unsafe: unsafeReasons.length > 0,
    unsafeReasons,
    popup,
  };
}

function childPath(parentPath: string, index: number): string {
  return `${parentPath}/iframe[${index}]`;
}

function parentStateFromDerivation(
  derivation: SandboxDerivation,
): ParentState {
  return {
    origin: derivation.origin.effective,
    flags: derivation.flags,
    resolutions: derivation.flagResolutions,
    path: derivation.path,
    sandboxActive: derivation.sandboxActive,
  };
}

function deriveChildren(
  children: readonly FrameInput[] | undefined,
  parent: ParentState,
): FrameTreeDerivation[] {
  if (!children || children.length === 0) {
    return [];
  }

  return children.map((child, index) => {
    const path = childPath(parent.path, index);
    const node = deriveNode(child, parent, path);
    return {
      ...node,
      children: deriveChildren(
        child.children,
        parentStateFromDerivation(node),
      ),
    };
  });
}

/**
 * Derive one frame from a minimal parent context. Use {@link deriveFrameTree}
 * when the nesting and origins are already known.
 */
export function deriveSandbox(
  frame: FrameInput,
  parentContext?: SandboxParentContext,
): SandboxDerivation {
  const path = frame.name ?? "frame";

  if (parentContext === undefined) {
    return deriveNode(frame, null, path);
  }

  const parentPath = parentContext.path ?? "parent";
  const parentOrigin = normalizeOrigin(
    parentContext.origin,
    "parent origin",
  );
  const parentTokens = parentContext.effectiveTokens
    ?? SANDBOX_CAPABILITY_TOKENS;
  const parentFlags = Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => {
      const token = CAPABILITY_KEY_TO_TOKEN[key];
      return [key, parentTokens.includes(token)];
    }),
  ) as unknown as SandboxFlagSet;
  const parentResolutions = Object.fromEntries(
    SANDBOX_CAPABILITY_KEYS.map((key) => {
      const allowed = parentFlags[key];
      return [
        key,
        {
          allowed,
          source: "inherited" as const,
          declaredAt: allowed ? parentPath : null,
        },
      ];
    }),
  ) as unknown as SandboxFlagResolutions;

  const parent: ParentState = {
    origin: parentOrigin,
    flags: parentFlags,
    resolutions: parentResolutions,
    path: parentPath,
    sandboxActive:
      parentContext.sandboxActive ??
      (parentContext.effectiveTokens !== undefined &&
        parentTokens.length < SANDBOX_CAPABILITY_TOKENS.length),
  };

  return deriveNode(frame, parent, path);
}

/**
 * Derive an entire nested iframe tree. Root capabilities default to all
 * enabled because an ordinary host page is unsandboxed.
 */
export function deriveFrameTree(
  root: RootFrameInput,
): FrameTreeDerivation {
  const rootPath = root.name ?? "root";
  const rootNode = deriveNode(
    { name: root.name, sandbox: root.sandbox },
    null,
    rootPath,
    root.origin,
  );

  return {
    ...rootNode,
    children: deriveChildren(
      root.children,
      parentStateFromDerivation(rootNode),
    ),
  };
}

/**
 * Derive a popup's initial sandbox from an opener. Popups inherit the opener's
 * effective flags unless `allow-popups-to-escape-sandbox` appears explicitly
 * on the opener frame.
 *
 * When `targetUrl` is supplied, the returned `origin` is the origin the popup
 * would have after navigating there. The initial popup document is otherwise
 * `about:blank` and inherits the opener origin.
 */
export function derivePopup(
  opener: SandboxDerivation,
  targetUrl?: string,
): PopupResolution {
  if (targetUrl === undefined || targetUrl.trim() === "") {
    return { ...opener.popup };
  }

  const trimmed = targetUrl.trim();
  const base =
    trimmed === "about:blank" || trimmed === "about:srcdoc"
      ? opener.origin.effective
      : resolveFrameOrigin(trimmed, opener.origin.effective);
  const effective = opener.popup.flags.sameOrigin
    ? base
    : OPAQUE_ORIGIN;

  return { ...opener.popup, origin: effective };
}

/**
 * Parse-only helper for callers that want the token view without inheritance.
 */
export { parseSandboxAttribute } from "./tokens.ts";

export {
  CAPABILITY_KEY_TO_TOKEN,
  CAPABILITY_TOKEN_TO_KEY,
  POPUP_ESCAPE_TOKEN,
  SANDBOX_CAPABILITY_KEYS,
  SANDBOX_CAPABILITY_TOKENS,
  SANDBOX_MODIFIER_TOKENS,
  SANDBOX_TOKENS,
} from "./tokens.ts";

export {
  OPAQUE_ORIGIN,
  isSameOrigin,
  normalizeOrigin,
  resolveFrameOrigin,
} from "./origin.ts";

export type {
  FrameInput,
  FrameTreeDerivation,
  OriginResolution,
  OriginSource,
  ParsedSandboxAttribute,
  PopupResolution,
  PopupSource,
  RootFrameInput,
  SandboxAttribute,
  SandboxCapabilityKey,
  SandboxCapabilityToken,
  SandboxDerivation,
  SandboxFlagResolution,
  SandboxFlagResolutions,
  SandboxFlagSet,
  SandboxFlagSource,
  SandboxModifierToken,
  SandboxParentContext,
  SandboxToken,
  UnsafeReason,
} from "./types.ts";
