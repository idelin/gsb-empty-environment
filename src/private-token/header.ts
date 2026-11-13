import { ChallengeParseError } from "./errors.ts";

export interface AuthChallenge {
  readonly scheme: string;
  readonly params: ReadonlyMap<string, string>;
}

export function parseWwwAuthenticate(header: string): AuthChallenge[] {
  const challenges: AuthChallenge[] = [];
  let index = 0;

  index = skipOws(header, index);
  while (index < header.length) {
    const schemeStart = index;
    index = skipToken(header, index);
    if (index === schemeStart) {
      throw new ChallengeParseError("Expected an authentication scheme in WWW-Authenticate");
    }
    const scheme = header.slice(schemeStart, index);
    const params = new Map<string, string>();

    index = skipOws(header, index);
    for (;;) {
      if (index >= header.length || header.charCodeAt(index) === 44) {
        break;
      }
      index = readAuthParam(header, index, params);
      index = skipOws(header, index);
      if (index >= header.length) {
        break;
      }
      if (header.charCodeAt(index) !== 44) {
        throw new ChallengeParseError("Malformed auth parameter list in WWW-Authenticate");
      }
      const afterComma = skipOws(header, index + 1);
      if (afterComma >= header.length || isNextChallenge(header, afterComma)) {
        break;
      }
      index = afterComma;
    }

    challenges.push({ scheme, params });
    index = skipOws(header, index);
    if (header.charCodeAt(index) === 44) {
      index += 1;
    }
    index = skipOws(header, index);
  }

  return challenges;
}

function readAuthParam(
  header: string,
  start: number,
  params: Map<string, string>,
): number {
  const nameStart = start;
  let index = skipToken(header, start);
  if (index === nameStart) {
    throw new ChallengeParseError("Malformed auth parameter in WWW-Authenticate");
  }
  const name = header.slice(nameStart, index).toLowerCase();
  index = skipOws(header, index);
  if (header.charCodeAt(index) !== 61) {
    throw new ChallengeParseError(
      "PrivateToken challenges use auth parameters; token68 form is not accepted",
    );
  }
  index += 1;
  index = skipOws(header, index);

  const quoted = readQuotedString(header, index);
  if (quoted !== undefined) {
    params.set(name, quoted.value);
    return quoted.next;
  }

  const valueStart = index;
  index = skipToken(header, index);
  if (index === valueStart) {
    throw new ChallengeParseError(`Auth parameter "${name}" has no value`);
  }
  params.set(name, header.slice(valueStart, index));
  return index;
}

function isNextChallenge(header: string, index: number): boolean {
  const tokenEnd = skipToken(header, index);
  if (tokenEnd === index) {
    return false;
  }
  const afterToken = skipOws(header, tokenEnd);
  return afterToken >= header.length || header.charCodeAt(afterToken) !== 61;
}

export function findPrivateTokenChallenge(header: string): string {
  const challenges = parseWwwAuthenticate(header);
  for (const challenge of challenges) {
    if (challenge.scheme.toLowerCase() !== "privatetoken") {
      continue;
    }
    const tokenChallenge = challenge.params.get("challenge");
    if (tokenChallenge === undefined) {
      throw new ChallengeParseError('PrivateToken challenge is missing the "challenge" parameter');
    }
    return tokenChallenge;
  }
  throw new ChallengeParseError("WWW-Authenticate header contains no PrivateToken challenge");
}

function readQuotedString(
  header: string,
  start: number,
): { value: string; next: number } | undefined {
  if (header.charCodeAt(start) !== 34) {
    return undefined;
  }

  let index = start + 1;
  let value = "";
  while (index < header.length) {
    const code = header.charCodeAt(index);
    if (code === 34) {
      return { value, next: index + 1 };
    }
    if (code === 92) {
      const escaped = header.charAt(index + 1);
      if (escaped === "") {
        throw new ChallengeParseError("Quoted string ends with a dangling escape");
      }
      value += escaped;
      index += 2;
      continue;
    }
    if (code === 13 || code === 10 || code === 0) {
      throw new ChallengeParseError("Quoted string contains an illegal control character");
    }
    value += header.charAt(index);
    index += 1;
  }

  throw new ChallengeParseError("Quoted string is missing its closing quote");
}

function skipToken(header: string, start: number): number {
  let index = start;
  while (index < header.length && isTChar(header.charCodeAt(index))) {
    index += 1;
  }
  return index;
}

function skipOws(header: string, start: number): number {
  let index = start;
  while (index < header.length) {
    const code = header.charCodeAt(index);
    if (code !== 32 && code !== 9) {
      break;
    }
    index += 1;
  }
  return index;
}

function isTChar(code: number): boolean {
  if (code >= 97 && code <= 122) {
    return true;
  }
  if (code >= 65 && code <= 90) {
    return true;
  }
  if (code >= 48 && code <= 57) {
    return true;
  }
  return (
    code === 33 ||
    code === 35 ||
    code === 36 ||
    code === 37 ||
    code === 38 ||
    code === 39 ||
    code === 42 ||
    code === 43 ||
    code === 45 ||
    code === 46 ||
    code === 94 ||
    code === 95 ||
    code === 96 ||
    code === 124 ||
    code === 126
  );
}
