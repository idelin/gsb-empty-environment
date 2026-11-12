import {
  collectHeaderValues,
  hasHeader,
  normalizeHostname,
  parseFirstStrictTransportSecurity,
  resolveRequestHost,
  type HeaderBag,
  type StrictTransportSecurityDirective,
} from './headers.ts';
import {
  LegacyPinStore,
  type LegacyPinPurgeResult,
} from './legacy-pin-store.ts';
import {
  StaticPinValidator,
  type ChainCertificate,
  type PinCheckResult,
  type StaticPinConfiguration,
  type StaticPinRule,
  type VerifyStaticPinsOptions,
} from './static-pins.ts';

export interface HstsState extends StrictTransportSecurityDirective {
  host: string;
  receivedAt: number;
  expiresAt: number;
  inheritedFrom?: string;
}

export interface IgnoredHeaderReportRequest {
  kind: 'hpkp' | 'hpkp-report-only' | 'expect-ct';
  uri: string;
  host: string;
}

export type ReportSender = (
  request: IgnoredHeaderReportRequest,
) => void | Promise<void>;

export interface NetworkSecurityPolicyOptions {
  staticPins?: StaticPinConfiguration;
  exemptUserInstalledRoots?: boolean;
  legacyPinStore?: LegacyPinStore | string;
  purgeLegacyPinsOnStartup?: boolean;
  reportSender?: ReportSender;
  now?: () => number;
}

export interface ResponseInput {
  url?: string | URL;
  host?: string;
  headers: HeaderBag;
  secure?: boolean;
  now?: number;
}

export interface HeaderProcessingResult {
  host: string;
  hsts: HstsState | null;
  hstsChanged: boolean;
  ignoredHeaders: readonly string[];
  reportRequests: readonly IgnoredHeaderReportRequest[];
}

export interface SecurityStateSnapshot {
  host: string;
  hsts: HstsState | null;
  staticPins: readonly StaticPinRule[];
  dynamicPins: readonly string[];
  expectCt: undefined;
  reportRequests: readonly IgnoredHeaderReportRequest[];
}

const EMPTY_REPORTS: readonly IgnoredHeaderReportRequest[] = Object.freeze([]);

function cloneHsts(state: HstsState): HstsState {
  return { ...state };
}

export class NetworkSecurityPolicy {
  private readonly staticPinValidator: StaticPinValidator;
  private readonly exemptUserInstalledRoots: boolean;
  private readonly hstsByHost = new Map<string, HstsState>();
  private readonly now: () => number;
  private readonly legacyPinStore: LegacyPinStore | null;
  private readonly reportSender?: ReportSender;
  readonly startupPinPurge: LegacyPinPurgeResult | null;

  constructor(options: NetworkSecurityPolicyOptions = {}) {
    this.staticPinValidator = new StaticPinValidator({
      staticPins: options.staticPins,
      exemptUserInstalledRoots: options.exemptUserInstalledRoots === true,
    });
    this.exemptUserInstalledRoots =
      options.exemptUserInstalledRoots === true;
    this.now = options.now ?? Date.now;
    this.reportSender = options.reportSender;
    this.legacyPinStore =
      typeof options.legacyPinStore === 'string'
        ? new LegacyPinStore(options.legacyPinStore)
        : options.legacyPinStore ?? null;

    if (this.legacyPinStore && options.purgeLegacyPinsOnStartup !== false) {
      this.startupPinPurge =
        this.legacyPinStore.purgeDynamicPinsAtStartup();
    } else {
      this.startupPinPurge = null;
    }
  }

  processResponse(
    input: ResponseInput | string | URL,
    headers?: HeaderBag,
  ): HeaderProcessingResult {
    let request: ResponseInput;
    if (typeof input === 'string' || input instanceof URL) {
      request = {
        url: input,
        headers: headers ?? {},
      };
    } else {
      request = input;
    }

    const resolved = request.url
      ? resolveRequestHost(request.url)
      : {
          host: normalizeHostname(request.host ?? ''),
          secure: true,
        };
    const host = normalizeHostname(request.host ?? resolved.host);
    const secure = request.secure ?? resolved.secure;
    const at = request.now ?? this.now();

    const ignoredHeaders: string[] = [];
    if (hasHeader(request.headers, 'public-key-pins')) {
      ignoredHeaders.push('public-key-pins');
    }
    if (hasHeader(request.headers, 'public-key-pins-report-only')) {
      ignoredHeaders.push('public-key-pins-report-only');
    }
    if (hasHeader(request.headers, 'expect-ct')) {
      ignoredHeaders.push('expect-ct');
    }

    let hstsChanged = false;
    if (host !== '' && secure) {
      const directive = parseFirstStrictTransportSecurity(
        collectHeaderValues(request.headers, 'strict-transport-security'),
      );
      if (directive) {
        if (directive.maxAge === 0) {
          this.hstsByHost.delete(host);
        } else {
          const expiresAt = at + directive.maxAge * 1000;
          this.hstsByHost.set(host, {
            host,
            maxAge: directive.maxAge,
            includeSubDomains: directive.includeSubDomains,
            preload: directive.preload,
            receivedAt: at,
            expiresAt,
          });
        }
        hstsChanged = true;
      }
    }

    return {
      host,
      hsts: this.getHsts(host, at),
      hstsChanged,
      ignoredHeaders,
      reportRequests: EMPTY_REPORTS,
    };
  }

  getHsts(hostValue: string, at = this.now()): HstsState | null {
    const host = normalizeHostname(hostValue);
    if (host === '') {
      return null;
    }

    const direct = this.hstsByHost.get(host);
    if (direct) {
      if (direct.expiresAt > at) {
        return cloneHsts(direct);
      }
      this.hstsByHost.delete(host);
    }

    let current = host;
    while (current.includes('.')) {
      current = current.slice(current.indexOf('.') + 1);
      const parent = this.hstsByHost.get(current);
      if (!parent) {
        continue;
      }
      if (parent.expiresAt <= at) {
        this.hstsByHost.delete(current);
        continue;
      }
      if (parent.includeSubDomains) {
        return cloneHsts({
          ...parent,
          host,
          inheritedFrom: parent.host,
        });
      }
    }

    return null;
  }

  getDynamicPins(_hostValue: string): readonly string[] {
    return [];
  }

  getExpectCtState(_hostValue: string): undefined {
    return undefined;
  }

  isExpectCtEnforced(_hostValue: string): boolean {
    return false;
  }

  getReportRequests(
    _hostValue?: string,
  ): readonly IgnoredHeaderReportRequest[] {
    return EMPTY_REPORTS;
  }

  getStaticPins(hostValue: string): readonly StaticPinRule[] {
    return this.staticPinValidator.rulesForHost(hostValue);
  }

  getSecurityState(hostValue: string, at = this.now()): SecurityStateSnapshot {
    const host = normalizeHostname(hostValue);
    return {
      host,
      hsts: this.getHsts(host, at),
      staticPins: this.staticPinValidator.rulesForHost(host),
      dynamicPins: [],
      expectCt: undefined,
      reportRequests: EMPTY_REPORTS,
    };
  }

  verifyCertificateChain(
    hostValue: string,
    chain: readonly ChainCertificate[],
    options: VerifyStaticPinsOptions = {},
  ): PinCheckResult {
    return this.staticPinValidator.verify(hostValue, chain, {
      exemptUserInstalledRoots:
        options.exemptUserInstalledRoots ??
        this.exemptUserInstalledRoots,
      trustAnchorUserInstalled: options.trustAnchorUserInstalled,
    });
  }

  purgeLegacyPinsOnStartup(): LegacyPinPurgeResult | null {
    return this.legacyPinStore?.purgeDynamicPinsAtStartup() ?? null;
  }

  getReportSender(): ReportSender | undefined {
    return this.reportSender;
  }
}

export function createNetworkSecurityPolicy(
  options: NetworkSecurityPolicyOptions = {},
): NetworkSecurityPolicy {
  return new NetworkSecurityPolicy(options);
}

export function processLegacySecurityHeaders(
  input: ResponseInput | string | URL,
  headers?: HeaderBag,
  options: NetworkSecurityPolicyOptions = {},
): HeaderProcessingResult {
  return new NetworkSecurityPolicy(options).processResponse(input, headers);
}

export { NetworkSecurityPolicy as LegacyHeaderPolicy };
export { NetworkSecurityPolicy as LegacyHeaderPolicyEngine };
