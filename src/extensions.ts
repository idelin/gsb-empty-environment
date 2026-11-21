export interface ExtensionOffer {
  name: string;
  params: string[];
}

function splitHeader(value: string): string[] {
  return value
    .split(',')
    .map((part) => part.trim())
    .filter((part) => part.length > 0);
}

export function parseSubprotocols(header: string | undefined): string[] {
  if (!header) {
    return [];
  }
  return splitHeader(header).map((value) => value.toLowerCase());
}

export function parseExtensions(header: string | undefined): ExtensionOffer[] {
  if (!header) {
    return [];
  }
  return splitHeader(header).map((offer) => {
    const tokens = offer
      .split(';')
      .map((token) => token.trim())
      .filter((token) => token.length > 0);
    const name = (tokens[0] ?? '').toLowerCase();
    return { name, params: tokens.slice(1) };
  });
}

export function negotiateSubprotocol(
  offered: string[],
  accepted: string[],
): string | null {
  const acceptedSet = new Set(accepted.map((value) => value.toLowerCase()));
  return offered.find((value) => acceptedSet.has(value)) ?? null;
}
