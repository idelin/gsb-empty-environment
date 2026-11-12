import { createHash, X509Certificate } from 'node:crypto';

import { normalizeHostname } from './headers.ts';

export interface StaticPinRule {
  host: string;
  pins: readonly string[];
  includeSubDomains: boolean;
}

export interface StaticPinRuleInput {
  host?: string;
  pins: readonly string[];
  includeSubDomains?: boolean;
}

export interface StaticPinRuleValue {
  pins: readonly string[];
  includeSubDomains?: boolean;
}

export type StaticPinConfiguration =
  | Readonly<
      Record<string, string | readonly string[] | StaticPinRuleValue>
    >
  | readonly StaticPinRuleInput[];

export interface CertificateReference {
  spkiSha256?: string;
  spkiDer?: Uint8Array | string;
  spki?: Uint8Array | string;
  certificate?: string | Uint8Array | X509Certificate;
  cert?: string | Uint8Array | X509Certificate;
  publicKey?: {
    export(options: { type: 'spki'; format: 'der' }): Uint8Array;
  };
  userInstalledRoot?: boolean;
  isUserInstalledRoot?: boolean;
  userInstalled?: boolean;
  trustAnchorUserInstalled?: boolean;
}

export type ChainCertificate =
  | string
  | Uint8Array
  | X509Certificate
  | CertificateReference;

export type PinCheckReason =
  | 'no-static-pins'
  | 'pin-match'
  | 'pin-mismatch'
  | 'user-installed-root-exempt'
  | 'empty-chain'
  | 'invalid-certificate';

export interface PinCheckResult {
  allowed: boolean;
  enforced: boolean;
  reason: PinCheckReason;
  host: string;
  matchedPin?: string;
  checkedPins: readonly string[];
}

export interface StaticPinValidatorOptions {
  staticPins?: StaticPinConfiguration;
  exemptUserInstalledRoots?: boolean;
}

export interface VerifyStaticPinsOptions {
  exemptUserInstalledRoots?: boolean;
  trustAnchorUserInstalled?: boolean;
}

function canonicalizePin(value: string): string {
  let pin = value.trim();
  pin = pin.replace(/^pin-sha256\s*=\s*/i, '');
  pin = pin.replace(/^sha256\//i, '');
  pin = pin.trim().replace(/^"(.*)"$/, '$1').trim();

  if (/^[a-f\d]{64}$/i.test(pin)) {
    return Buffer.from(pin, 'hex').toString('base64');
  }

  try {
    const decoded = Buffer.from(
      pin.replace(/-/g, '+').replace(/_/g, '/'),
      'base64',
    );
    if (decoded.length === 32) {
      return decoded.toString('base64');
    }
  } catch {
    return pin;
  }

  return pin;
}

function normalizePins(pins: readonly string[]): string[] {
  if (!Array.isArray(pins) || pins.length === 0) {
    throw new TypeError('A static pin rule must contain at least one pin');
  }

  const normalized = pins.map((pin) => {
    if (typeof pin !== 'string' || pin.trim() === '') {
      throw new TypeError('Static pins must be non-empty strings');
    }
    return canonicalizePin(pin);
  });

  return Array.from(new Set(normalized));
}

function normalizeRules(
  configuration: StaticPinConfiguration | undefined,
): StaticPinRule[] {
  if (!configuration) {
    return [];
  }

  const rules: StaticPinRule[] = [];

  if (Array.isArray(configuration)) {
    for (const entry of configuration as readonly StaticPinRuleInput[]) {
      if (!entry || typeof entry !== 'object' || !entry.host) {
        throw new TypeError('Each static pin rule requires a host');
      }
      rules.push({
        host: normalizeHostname(entry.host),
        pins: normalizePins(entry.pins),
        includeSubDomains: entry.includeSubDomains === true,
      });
    }
  } else {
    for (const [host, entry] of Object.entries(configuration)) {
      if (typeof entry === 'string') {
        rules.push({
          host: normalizeHostname(host),
          pins: normalizePins([entry]),
          includeSubDomains: false,
        });
      } else if (Array.isArray(entry)) {
        rules.push({
          host: normalizeHostname(host),
          pins: normalizePins(entry),
          includeSubDomains: false,
        });
      } else if (entry && typeof entry === 'object') {
        rules.push({
          host: normalizeHostname(host),
          pins: normalizePins(entry.pins),
          includeSubDomains: entry.includeSubDomains === true,
        });
      } else {
        throw new TypeError(`Invalid static pin entry for ${host}`);
      }
    }
  }

  const deduplicated = new Map<string, StaticPinRule>();
  for (const rule of rules) {
    deduplicated.set(rule.host, rule);
  }
  return Array.from(deduplicated.values());
}

function digestSpki(der: Uint8Array | string): string {
  return createHash('sha256').update(der).digest('base64');
}

function certificateSpkiPin(certificate: ChainCertificate): string {
  if (certificate instanceof X509Certificate) {
    const der = certificate.publicKey.export({ type: 'spki', format: 'der' });
    return digestSpki(der);
  }

  if (typeof certificate === 'string' || certificate instanceof Uint8Array) {
    const parsed = new X509Certificate(certificate as string | Buffer);
    const der = parsed.publicKey.export({ type: 'spki', format: 'der' });
    return digestSpki(der);
  }

  if (certificate && typeof certificate === 'object') {
    if (typeof certificate.spkiSha256 === 'string') {
      return canonicalizePin(certificate.spkiSha256);
    }
    if (certificate.spkiDer != null) {
      return digestSpki(certificate.spkiDer);
    }
    if (certificate.spki != null) {
      return digestSpki(certificate.spki);
    }
    if (certificate.certificate != null) {
      return canonicalizePin(
        certificateSpkiPin(certificate.certificate as ChainCertificate),
      );
    }
    if (certificate.cert != null) {
      return canonicalizePin(
        certificateSpkiPin(certificate.cert as ChainCertificate),
      );
    }
    if (certificate.publicKey?.export) {
      return digestSpki(
        certificate.publicKey.export({ type: 'spki', format: 'der' }),
      );
    }
  }

  throw new TypeError('Unsupported certificate reference');
}

function chainHasUserInstalledRoot(
  chain: readonly ChainCertificate[],
  explicit: boolean | undefined,
): boolean {
  if (explicit === true) {
    return true;
  }

  if (chain.length === 0) {
    return false;
  }

  const root = chain[chain.length - 1];
  if (!root || typeof root !== 'object' || root instanceof Uint8Array) {
    return false;
  }
  if (root instanceof X509Certificate) {
    return false;
  }

  const anchor = root as CertificateReference;
  return (
    anchor.userInstalledRoot === true ||
    anchor.isUserInstalledRoot === true ||
    anchor.userInstalled === true ||
    anchor.trustAnchorUserInstalled === true
  );
}

export function computeSpkiPin(certificate: ChainCertificate): string {
  return certificateSpkiPin(certificate);
}

export class StaticPinValidator {
  private readonly rules: readonly StaticPinRule[];
  private readonly exemptUserInstalledRoots: boolean;

  constructor(options: StaticPinValidatorOptions = {}) {
    this.rules = normalizeRules(options.staticPins);
    this.exemptUserInstalledRoots =
      options.exemptUserInstalledRoots === true;
  }

  getRules(): readonly StaticPinRule[] {
    return this.rules;
  }

  findRule(hostValue: string): StaticPinRule | null {
    const host = normalizeHostname(hostValue);
    let best: StaticPinRule | null = null;

    for (const rule of this.rules) {
      const matches =
        host === rule.host ||
        (rule.includeSubDomains && host.endsWith(`.${rule.host}`));
      if (!matches) {
        continue;
      }
      if (!best || rule.host.length > best.host.length) {
        best = rule;
      }
    }

    return best;
  }

  rulesForHost(hostValue: string): readonly StaticPinRule[] {
    const host = normalizeHostname(hostValue);
    return this.rules.filter(
      (rule) =>
        host === rule.host ||
        (rule.includeSubDomains && host.endsWith(`.${rule.host}`)),
    );
  }

  verify(
    hostValue: string,
    chain: readonly ChainCertificate[],
    options: VerifyStaticPinsOptions = {},
  ): PinCheckResult {
    const host = normalizeHostname(hostValue);
    const rule = this.findRule(host);

    if (!rule) {
      return {
        allowed: true,
        enforced: false,
        reason: 'no-static-pins',
        host,
        checkedPins: [],
      };
    }

    if (chain.length === 0) {
      return {
        allowed: false,
        enforced: true,
        reason: 'empty-chain',
        host,
        checkedPins: rule.pins,
      };
    }

    const exempt =
      options.exemptUserInstalledRoots ?? this.exemptUserInstalledRoots;
    if (
      exempt &&
      chainHasUserInstalledRoot(chain, options.trustAnchorUserInstalled)
    ) {
      return {
        allowed: true,
        enforced: false,
        reason: 'user-installed-root-exempt',
        host,
        checkedPins: rule.pins,
      };
    }

    let chainPins: string[];
    try {
      chainPins = chain.map((certificate) =>
        canonicalizePin(certificateSpkiPin(certificate)),
      );
    } catch {
      return {
        allowed: false,
        enforced: true,
        reason: 'invalid-certificate',
        host,
        checkedPins: rule.pins,
      };
    }

    const matchedPin = rule.pins.find((pin) => chainPins.includes(pin));
    if (matchedPin) {
      return {
        allowed: true,
        enforced: true,
        reason: 'pin-match',
        host,
        matchedPin,
        checkedPins: rule.pins,
      };
    }

    return {
      allowed: false,
      enforced: true,
      reason: 'pin-mismatch',
      host,
      checkedPins: rule.pins,
    };
  }
}
