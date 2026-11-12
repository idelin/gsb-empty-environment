/**
 * A capability that is enabled by a positive `allow-*` token. The sandbox
 * attribute is a default-deny list: the absence of a token removes the
 * corresponding capability.
 */
export type SandboxCapabilityToken =
  | "allow-downloads"
  | "allow-downloads-without-user-activation"
  | "allow-forms"
  | "allow-modals"
  | "allow-orientation-lock"
  | "allow-pointer-lock"
  | "allow-popups"
  | "allow-presentation"
  | "allow-same-origin"
  | "allow-scripts"
  | "allow-storage-access-by-user-activation"
  | "allow-top-navigation"
  | "allow-top-navigation-by-user-activation"
  | "allow-top-navigation-to-custom-protocols";

/**
 * A modifier token. It does not grant a capability by itself; it changes how
 * the next browsing context (a popup) is sandboxed.
 */
export type SandboxModifierToken = "allow-popups-to-escape-sandbox";

export type SandboxToken = SandboxCapabilityToken | SandboxModifierToken;

/**
 * The type accepted wherever a frame's sandbox attribute is described.
 *
 * - `null` / `undefined` means the attribute is absent and the frame inherits
 *   the parent's effective sandbox.
 * - `""` means the attribute is present with no tokens (the most restrictive
 *   sandbox).
 * - A string is parsed as an ASCII-whitespace-separated token list.
 * - An array is treated as a token list. An empty array is an empty sandbox.
 */
export type SandboxAttribute = string | readonly string[] | null | undefined;

export type SandboxCapabilityKey =
  | "downloads"
  | "downloadsWithoutUserActivation"
  | "forms"
  | "modals"
  | "orientationLock"
  | "pointerLock"
  | "popups"
  | "presentation"
  | "sameOrigin"
  | "scripts"
  | "storageAccessByUserActivation"
  | "topNavigation"
  | "topNavigationByUserActivation"
  | "topNavigationToCustomProtocols";

export type SandboxFlagSet = Record<SandboxCapabilityKey, boolean>;

/**
 * Why a capability is enabled or disabled.
 *
 * `root` is the host document's default (an unsandboxed host permits every
 * capability). `inherited` means the frame has no sandbox attribute and kept
 * the parent's result. `declared` means this frame declared the token and the
 * parent also permitted it. `omitted` means this frame's attribute did not
 * contain the token. `blocked-ancestor` means the frame declared the token but
 * an ancestor had already removed it.
 */
export type SandboxFlagSource =
  | "root"
  | "inherited"
  | "declared"
  | "omitted"
  | "blocked-ancestor";

export interface SandboxFlagResolution {
  allowed: boolean;
  source: SandboxFlagSource;
  /** Path of the frame whose declaration last enabled the capability. */
  declaredAt: string | null;
}

export type SandboxFlagResolutions = Record<
  SandboxCapabilityKey,
  SandboxFlagResolution
>;

export interface ParsedSandboxAttribute {
  /** Whether a sandbox attribute was supplied at all. */
  present: boolean;
  /** Normalized, de-duplicated capability tokens in input order. */
  tokens: SandboxCapabilityToken[];
  /** Recognized modifier tokens (currently only the popup escape token). */
  modifiers: SandboxModifierToken[];
  /** Lower-cased tokens that the sandbox grammar does not recognize. */
  unknownTokens: string[];
}

export type OriginSource = "root" | "url" | "srcdoc" | "about-blank";

export interface OriginResolution {
  /** Provenance of the origin before sandboxing is applied. */
  source: OriginSource;
  /**
   * Serialized origin before the sandbox is applied. This is the parent's
   * effective origin for `srcdoc` and `about:blank`, and `"opaque"` for URLs
   * whose origin is intrinsically opaque (for example, `data:`).
   */
  base: string;
  /** Serialized origin after `allow-same-origin` has been considered. */
  effective: string;
  /** True when the sandbox forces an otherwise concrete origin to be opaque. */
  forcedOpaque: boolean;
}

export type UnsafeReason =
  | "same-origin-scripts-can-remove-sandbox"
  | "popup-escape-bypasses-sandbox";

export type PopupSource = "blocked" | "inherited" | "escaped";

export interface PopupResolution {
  /** Whether the opener's effective flags still contain `allow-popups`. */
  allowed: boolean;
  source: PopupSource;
  /**
   * Whether the escape token appeared on this frame itself. It is never
   * inherited from an ancestor.
   */
  escapeTokenExplicit: boolean;
  /** True only when the popup is allowed and the escape token is explicit. */
  escaped: boolean;
  /** Capabilities inherited by the popup. Empty when the popup is blocked. */
  effectiveTokens: SandboxCapabilityToken[];
  flags: SandboxFlagSet;
  /** Serialized origin of the initial `about:blank` popup document. */
  origin: string;
  /** Whether the popup inherits a sandbox. Escaped popups are unsandboxed. */
  sandboxed: boolean;
  /** True when a script-capable opener can open an unsandboxed popup. */
  unsafe: boolean;
}

export interface SandboxDerivation {
  path: string;
  name: string | null;
  hasParent: boolean;
  /** Whether a sandbox attribute was supplied on this frame. */
  sandboxAttributePresent: boolean;
  /** True when this frame or an ancestor supplied a sandbox attribute. */
  sandboxActive: boolean;
  /** Capability and modifier tokens declared here, in input order. */
  declaredTokens: SandboxToken[];
  modifiers: SandboxModifierToken[];
  unknownTokens: string[];
  /** Capabilities that survive inheritance, in canonical token order. */
  effectiveTokens: SandboxCapabilityToken[];
  /** Convenience boolean view of {@link effectiveTokens}. */
  flags: SandboxFlagSet;
  /** Per-capability provenance and denial explanation. */
  flagResolutions: SandboxFlagResolutions;
  /** Escape token explicitly present on this frame (never inherited). */
  popupEscapeTokenExplicit: boolean;
  origin: OriginResolution;
  sameOriginAsParent: boolean;
  unsafe: boolean;
  unsafeReasons: UnsafeReason[];
  popup: PopupResolution;
}

export interface FrameInput {
  name?: string;
  /** Frame URL. Relative URLs resolve against the parent's effective origin. */
  src?: string;
  /**
   * Inline document. When both `srcdoc` and `src` are supplied, `srcdoc` wins,
   * matching the HTML iframe processing model.
   */
  srcdoc?: string;
  sandbox?: SandboxAttribute;
  children?: readonly FrameInput[];
}

export interface RootFrameInput {
  /** Serialized origin of the host document. */
  origin: string;
  name?: string;
  /** Optional sandbox on the root document itself. Normally omitted. */
  sandbox?: SandboxAttribute;
  children?: readonly FrameInput[];
}

export interface FrameTreeDerivation extends SandboxDerivation {
  children: FrameTreeDerivation[];
}

/**
 * Minimal parent description for direct single-frame derivation. When
 * `effectiveTokens` is omitted, the parent is treated as unsandboxed.
 */
export interface SandboxParentContext {
  /** Parent document's effective origin. */
  origin: string;
  effectiveTokens?: readonly SandboxCapabilityToken[];
  /** True if the parent document itself (or an ancestor) is sandboxed. */
  sandboxActive?: boolean;
  /** Path used in provenance records. Defaults to `"parent"`. */
  path?: string;
}
