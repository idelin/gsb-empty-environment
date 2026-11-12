import type {
  ParsedSandboxAttribute,
  SandboxAttribute,
  SandboxCapabilityKey,
  SandboxCapabilityToken,
  SandboxModifierToken,
  SandboxToken,
} from "./types.ts";

export const SANDBOX_CAPABILITY_TOKENS = [
  "allow-downloads",
  "allow-downloads-without-user-activation",
  "allow-forms",
  "allow-modals",
  "allow-orientation-lock",
  "allow-pointer-lock",
  "allow-popups",
  "allow-presentation",
  "allow-same-origin",
  "allow-scripts",
  "allow-storage-access-by-user-activation",
  "allow-top-navigation",
  "allow-top-navigation-by-user-activation",
  "allow-top-navigation-to-custom-protocols",
] as const satisfies readonly SandboxCapabilityToken[];

export const POPUP_ESCAPE_TOKEN =
  "allow-popups-to-escape-sandbox" as const satisfies SandboxModifierToken;

export const SANDBOX_MODIFIER_TOKENS = [POPUP_ESCAPE_TOKEN] as const;

export const SANDBOX_TOKENS = [
  ...SANDBOX_CAPABILITY_TOKENS,
  ...SANDBOX_MODIFIER_TOKENS,
] as const;

export const SANDBOX_CAPABILITY_KEYS = [
  "downloads",
  "downloadsWithoutUserActivation",
  "forms",
  "modals",
  "orientationLock",
  "pointerLock",
  "popups",
  "presentation",
  "sameOrigin",
  "scripts",
  "storageAccessByUserActivation",
  "topNavigation",
  "topNavigationByUserActivation",
  "topNavigationToCustomProtocols",
] as const satisfies readonly SandboxCapabilityKey[];

export const CAPABILITY_TOKEN_TO_KEY: Record<
  SandboxCapabilityToken,
  SandboxCapabilityKey
> = {
  "allow-downloads": "downloads",
  "allow-downloads-without-user-activation":
    "downloadsWithoutUserActivation",
  "allow-forms": "forms",
  "allow-modals": "modals",
  "allow-orientation-lock": "orientationLock",
  "allow-pointer-lock": "pointerLock",
  "allow-popups": "popups",
  "allow-presentation": "presentation",
  "allow-same-origin": "sameOrigin",
  "allow-scripts": "scripts",
  "allow-storage-access-by-user-activation":
    "storageAccessByUserActivation",
  "allow-top-navigation": "topNavigation",
  "allow-top-navigation-by-user-activation":
    "topNavigationByUserActivation",
  "allow-top-navigation-to-custom-protocols":
    "topNavigationToCustomProtocols",
};

export const CAPABILITY_KEY_TO_TOKEN: Record<
  SandboxCapabilityKey,
  SandboxCapabilityToken
> = Object.fromEntries(
  Object.entries(CAPABILITY_TOKEN_TO_KEY).map(([token, key]) => [key, token]),
) as unknown as Record<SandboxCapabilityKey, SandboxCapabilityToken>;

const CAPABILITY_TOKEN_SET: ReadonlySet<string> = new Set(
  SANDBOX_CAPABILITY_TOKENS,
);
const MODIFIER_TOKEN_SET: ReadonlySet<string> = new Set(
  SANDBOX_MODIFIER_TOKENS,
);
const KNOWN_TOKEN_SET: ReadonlySet<string> = new Set(SANDBOX_TOKENS);

function rawTokenParts(attribute: SandboxAttribute): string[] {
  if (attribute === null || attribute === undefined) {
    return [];
  }

  const input =
    typeof attribute === "string" ? attribute : attribute.join(" ");

  return input
    .split(/[ \t\n\f\r]+/)
    .filter((part) => part.length > 0)
    .map((part) => part.toLowerCase());
}

/**
 * Parse a sandbox attribute without interpreting inheritance. Unknown tokens
 * are reported and ignored, as browsers do.
 */
export function parseSandboxAttribute(
  attribute: SandboxAttribute,
): ParsedSandboxAttribute {
  const present = attribute !== null && attribute !== undefined;
  const uniqueParts = [...new Set(rawTokenParts(attribute))];
  const tokens: SandboxCapabilityToken[] = [];
  const modifiers: SandboxModifierToken[] = [];
  const unknownTokens: string[] = [];

  for (const part of uniqueParts) {
    if (!KNOWN_TOKEN_SET.has(part)) {
      unknownTokens.push(part);
      continue;
    }

    if (MODIFIER_TOKEN_SET.has(part)) {
      modifiers.push(part as SandboxModifierToken);
      continue;
    }

    if (CAPABILITY_TOKEN_SET.has(part)) {
      tokens.push(part as SandboxCapabilityToken);
    }
  }

  return {
    present,
    tokens,
    modifiers,
    unknownTokens,
  };
}

export function hasCapabilityToken(
  parsed: ParsedSandboxAttribute,
  token: SandboxCapabilityToken,
): boolean {
  return parsed.tokens.includes(token);
}

export function hasPopupEscapeToken(
  parsed: ParsedSandboxAttribute,
): boolean {
  return parsed.modifiers.includes(POPUP_ESCAPE_TOKEN);
}
