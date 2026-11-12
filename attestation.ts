import { Buffer } from "node:buffer";

export type AttestationConveyancePreference =
  | "none"
  | "indirect"
  | "direct"
  | "enterprise";

export interface RegistrationAttestationRequest {
  rpId: string;
  attestation?: AttestationConveyancePreference | string | Record<string, unknown>;
  attestationFormats?: readonly string[];
  requestedAttestationFormats?: readonly string[];
  enterpriseAttestationFormat?: string;
  enterpriseAttestationFormats?: readonly string[];
  formats?: readonly string[];
  enterpriseAttestation?: boolean | Record<string, unknown>;
  enterprise?: boolean;
}

export interface RegistrationAttestationResponse {
  attestationObject?:
    | string
    | Uint8Array
    | ArrayBuffer
    | Record<string, unknown>;
  fmt?: string;
  attestationFormat?: string;
  attestation?: Record<string, unknown>;
  enterpriseAttestation?: Record<string, unknown>;
  attStmt?: Record<string, unknown> | Map<unknown, unknown>;
  authData?: string | Uint8Array | ArrayBuffer | Record<string, unknown>;
  certificate?: string | Uint8Array | ArrayBuffer | Record<string, unknown>;
  certificateAaguid?: string | Uint8Array;
  x5c?: readonly (string | Uint8Array | Record<string, unknown>)[];
  serialNumber?: string;
}

export interface RegistrationCompletionEvent {
  request: RegistrationAttestationRequest;
  response: RegistrationAttestationResponse;
  policy?: AttestationFilterPolicy;
}

export interface AttestationFilterPolicy {
  adminRpIds?: readonly string[];
  allowedRpIds?: readonly string[];
  trustedRpIds?: readonly string[];
  rpIds?: readonly string[];
  allowedRpId?: string | readonly string[];
  managedAaguids?: readonly string[];
  managedAAGUIDs?: readonly string[];
  managedAAGUID?: string | readonly string[];
  managedAaguid?: string | readonly string[];
  managedInventory?: readonly string[];
  aaguidAllowlist?: readonly string[];
  aaguidWhitelist?: readonly string[];
  aaguidInventory?: readonly string[];
  managedAaguidList?: readonly string[];
  rpIdAllowlist?: readonly string[];
  rpIdWhitelist?: readonly string[];
  enterpriseRpIds?: readonly string[];
  enterpriseFormats?: readonly string[];
  allowedFormats?: readonly string[];
  attestationFormats?: readonly string[];
}

export type DowngradeReason =
  | "rp-id-not-allowed"
  | "enterprise-attestation-not-requested"
  | "missing-attestation-response"
  | "attestation-format-missing"
  | "attestation-format-mismatch"
  | "attestation-format-not-allowed"
  | "missing-authenticator-aaguid"
  | "missing-attestation-certificate"
  | "missing-certificate-aaguid"
  | "aaguid-mismatch"
  | "aaguid-not-managed"
  | "missing-serial-number";

export interface FilteredRegistrationAttestation {
  attestation: "enterprise" | "none";
  fmt: string;
  certificate: string | null;
  serialNumber: string | null;
  aaguid: string | null;
  downgradeReason: DowngradeReason | null;
  readonly accepted?: boolean;
  readonly attestationType?: "enterprise" | "none";
  readonly attestationFormat?: string;
  readonly cert?: string | null;
  readonly serial?: string | null;
  readonly reason?: DowngradeReason | null;
  readonly record?: FilteredRegistrationAttestation;
  readonly persisted?: FilteredRegistrationAttestation;
}

interface NormalizedPolicy {
  rpIds: Set<string>;
  managedAaguids: Set<string>;
  enterpriseFormats: Set<string> | null;
}

interface NormalizedEvent {
  request: RegistrationAttestationRequest;
  response: RegistrationAttestationResponse;
}

interface ParsedAttestation {
  fmt: string | null;
  attStmt: unknown;
  authData: unknown;
}

interface CertificateInfo {
  der: Uint8Array | null;
  aaguid: Uint8Array | null;
  subjectSerialNumber: string | null;
  certificateSerialNumber: string | null;
  storedValue: string | null;
}

const AAGUID_EXTENSION_OID = "1.3.6.1.4.1.45724.1.1.4";
const SUBJECT_SERIAL_NUMBER_OID = "2.5.4.5";
const UUID_PATTERN =
  /^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export class EnterpriseAttestationFilter {
  private readonly policy: NormalizedPolicy;

  constructor(
    policy: AttestationFilterPolicy | readonly string[] = {},
    managedAaguids?: readonly string[],
  ) {
    const source: AttestationFilterPolicy = Array.isArray(policy)
      ? { adminRpIds: policy, managedAaguids }
      : policy;
    this.policy = normalizePolicy(source);
  }

  filter(
    event: RegistrationCompletionEvent,
    policy?: AttestationFilterPolicy,
  ): FilteredRegistrationAttestation;
  filter(
    request: RegistrationAttestationRequest,
    response: RegistrationAttestationResponse,
    policy?: AttestationFilterPolicy,
  ): FilteredRegistrationAttestation;
  filter(
    eventOrRequest: RegistrationCompletionEvent | RegistrationAttestationRequest,
    responseOrPolicy?: RegistrationAttestationResponse | AttestationFilterPolicy,
    maybePolicy?: AttestationFilterPolicy,
  ): FilteredRegistrationAttestation {
    const { event, policy } = normalizeFilterArguments(
      eventOrRequest,
      responseOrPolicy,
      maybePolicy,
      this.policy,
    );
    return filterWithNormalizedPolicy(event, policy);
  }

  onRegistrationComplete(
    event: RegistrationCompletionEvent,
  ): FilteredRegistrationAttestation;
  onRegistrationComplete(
    request: RegistrationAttestationRequest,
    response: RegistrationAttestationResponse,
  ): FilteredRegistrationAttestation;
  onRegistrationComplete(
    eventOrRequest: RegistrationCompletionEvent | RegistrationAttestationRequest,
    response?: RegistrationAttestationResponse,
  ): FilteredRegistrationAttestation {
    return this.filter(eventOrRequest as RegistrationCompletionEvent, response);
  }
}

export function filterEnterpriseAttestation(
  event: RegistrationCompletionEvent,
  policy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation;
export function filterEnterpriseAttestation(
  request: RegistrationAttestationRequest,
  response: RegistrationAttestationResponse,
  policy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation;
export function filterEnterpriseAttestation(
  eventOrRequest: RegistrationCompletionEvent | RegistrationAttestationRequest,
  responseOrPolicy?: RegistrationAttestationResponse | AttestationFilterPolicy,
  maybePolicy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation {
  const { event, policy } = normalizeFilterArguments(
    eventOrRequest,
    responseOrPolicy,
    maybePolicy,
    normalizePolicy({}),
  );
  return filterWithNormalizedPolicy(event, policy);
}

export function filterRegistrationAttestation(
  event: RegistrationCompletionEvent,
  policy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation;
export function filterRegistrationAttestation(
  request: RegistrationAttestationRequest,
  response: RegistrationAttestationResponse,
  policy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation;
export function filterRegistrationAttestation(
  eventOrRequest: RegistrationCompletionEvent | RegistrationAttestationRequest,
  responseOrPolicy?: RegistrationAttestationResponse | AttestationFilterPolicy,
  maybePolicy?: AttestationFilterPolicy,
): FilteredRegistrationAttestation {
  return filterEnterpriseAttestation(
    eventOrRequest as RegistrationCompletionEvent,
    responseOrPolicy as RegistrationAttestationResponse,
    maybePolicy as AttestationFilterPolicy,
  );
}

export const onRegistrationComplete = filterRegistrationAttestation;
export const applyEnterpriseAttestationPolicy = filterEnterpriseAttestation;
export const filterAttestation = filterEnterpriseAttestation;
export const filterRegistration = filterEnterpriseAttestation;
export const handleRegistrationComplete = filterRegistrationAttestation;

export function createAttestationFilter(
  policy: AttestationFilterPolicy | readonly string[],
  managedAaguids?: readonly string[],
): EnterpriseAttestationFilter {
  return new EnterpriseAttestationFilter(policy, managedAaguids);
}

export function createRegistrationCompletionHandler(
  policy: AttestationFilterPolicy,
  save?: (record: FilteredRegistrationAttestation) => void,
): (event: RegistrationCompletionEvent) => FilteredRegistrationAttestation {
  const filter = new EnterpriseAttestationFilter(policy);
  return (event) => {
    const record = filter.onRegistrationComplete(event);
    if (save) save(record);
    return record;
  };
}

export const AttestationFilter = EnterpriseAttestationFilter;

function filterWithNormalizedPolicy(
  event: NormalizedEvent,
  policy: NormalizedPolicy,
): FilteredRegistrationAttestation {
  const { request, response } = event;
  const rpId = normalizeRpId(request?.rpId);

  if (!rpId || !policy.rpIds.has(rpId)) {
    return downgrade("rp-id-not-allowed");
  }

  const requestedFormats = collectRequestedFormats(request);
  if (!isEnterpriseRequested(request)) {
    return downgrade("enterprise-attestation-not-requested");
  }

  let attestation: ParsedAttestation;
  try {
    attestation = parseAttestationResponse(response);
  } catch {
    return downgrade("missing-attestation-response");
  }

  if (!attestation.fmt) {
    return downgrade("attestation-format-missing");
  }

  const fmt = attestation.fmt.toLowerCase();
  if (requestedFormats.size > 0 && !requestedFormats.has(fmt)) {
    return downgrade("attestation-format-mismatch");
  }
  if (
    requestedFormats.size === 0 &&
    policy.enterpriseFormats !== null &&
    !policy.enterpriseFormats.has(fmt)
  ) {
    return downgrade("attestation-format-not-allowed");
  }

  const authenticatorAaguid = extractAuthenticatorAaguid(attestation.authData);
  if (!authenticatorAaguid) {
    return downgrade("missing-authenticator-aaguid");
  }

  const certificate = extractCertificateInfo(response, attestation.attStmt);
  if (!certificate.storedValue) {
    return downgrade("missing-attestation-certificate");
  }

  if (!certificate.aaguid) {
    return downgrade("missing-certificate-aaguid");
  }

  const authenticatorAaguidText = aaguidToUuid(authenticatorAaguid);
  const certificateAaguidText = aaguidToUuid(certificate.aaguid);
  if (authenticatorAaguidText !== certificateAaguidText) {
    return downgrade("aaguid-mismatch");
  }

  if (!policy.managedAaguids.has(authenticatorAaguidText)) {
    return downgrade("aaguid-not-managed");
  }

  const serialNumber =
    certificate.subjectSerialNumber ??
    certificate.certificateSerialNumber ??
    normalizeSerialNumber(response?.serialNumber);
  if (!serialNumber) {
    return downgrade("missing-serial-number");
  }

  return finalizeRecord({
    attestation: "enterprise",
    fmt,
    certificate: certificate.storedValue,
    serialNumber,
    aaguid: authenticatorAaguidText,
    downgradeReason: null,
  });
}

function downgrade(
  reason: DowngradeReason,
): FilteredRegistrationAttestation {
  return finalizeRecord({
    attestation: "none",
    fmt: "none",
    certificate: null,
    serialNumber: null,
    aaguid: null,
    downgradeReason: reason,
  });
}

function finalizeRecord(
  record: FilteredRegistrationAttestation,
): FilteredRegistrationAttestation {
  Object.defineProperties(record, {
    accepted: {
      enumerable: false,
      get: () => record.attestation === "enterprise",
    },
    attestationType: {
      enumerable: false,
      get: () => record.attestation,
    },
    attestationFormat: {
      enumerable: false,
      get: () => record.fmt,
    },
    cert: {
      enumerable: false,
      get: () => record.certificate,
    },
    serial: {
      enumerable: false,
      get: () => record.serialNumber,
    },
    reason: {
      enumerable: false,
      get: () => record.downgradeReason,
    },
    record: {
      enumerable: false,
      get: () => record,
    },
    persisted: {
      enumerable: false,
      get: () => record,
    },
  });
  return record;
}

function normalizeFilterArguments(
  eventOrRequest: RegistrationCompletionEvent | RegistrationAttestationRequest,
  responseOrPolicy: RegistrationAttestationResponse | AttestationFilterPolicy | undefined,
  maybePolicy: AttestationFilterPolicy | undefined,
  fallbackPolicy: NormalizedPolicy,
): { event: NormalizedEvent; policy: NormalizedPolicy } {
  if (maybePolicy !== undefined) {
    return {
      event: {
        request: eventOrRequest as RegistrationAttestationRequest,
        response: responseOrPolicy as RegistrationAttestationResponse,
      },
      policy: normalizePolicy(maybePolicy),
    };
  }

  const possibleEvent = eventOrRequest as RegistrationCompletionEvent & {
    options?: RegistrationAttestationRequest;
    credential?: { response?: RegistrationAttestationResponse };
    rpId?: string;
    policy?: AttestationFilterPolicy;
  };

  if (possibleEvent.request || possibleEvent.options || possibleEvent.credential) {
    const request =
      possibleEvent.request ??
      possibleEvent.options ??
      ({
        rpId: possibleEvent.rpId ?? "",
      } as RegistrationAttestationRequest);
    const response =
      possibleEvent.response ??
      possibleEvent.credential?.response ??
      ({} as RegistrationAttestationResponse);
    return {
      event: { request, response },
      policy:
        responseOrPolicy === undefined
          ? possibleEvent.policy
            ? normalizePolicy(possibleEvent.policy)
            : fallbackPolicy
          : normalizePolicy(responseOrPolicy as AttestationFilterPolicy),
    };
  }

  return {
    event: {
      request: eventOrRequest as RegistrationAttestationRequest,
      response: responseOrPolicy as RegistrationAttestationResponse,
    },
    policy:
      responseOrPolicy === undefined
        ? fallbackPolicy
        : normalizePolicy(responseOrPolicy as AttestationFilterPolicy),
  };
}

function normalizePolicy(policy: AttestationFilterPolicy): NormalizedPolicy {
  const source = policy ?? {};
  const rpIds = new Set<string>();
  const addRpIds = (value: unknown) => {
    for (const entry of iterableStrings(value)) {
      const normalized = normalizeRpId(entry);
      if (normalized) rpIds.add(normalized);
    }
  };

  addRpIds(source.adminRpIds);
  addRpIds(source.allowedRpIds);
  addRpIds(source.trustedRpIds);
  addRpIds(source.rpIds);
  addRpIds(source.allowedRpId);
  addRpIds(source.rpIdAllowlist);
  addRpIds(source.rpIdWhitelist);
  addRpIds(source.enterpriseRpIds);

  const managedAaguids = new Set<string>();
  const addAaguids = (value: unknown) => {
    for (const entry of iterableStrings(value)) {
      const normalized = normalizeAaguid(entry);
      if (normalized) managedAaguids.add(normalized);
    }
  };

  addAaguids(source.managedAaguids);
  addAaguids(source.managedAAGUIDs);
  addAaguids(source.managedAAGUID);
  addAaguids(source.managedAaguid);
  addAaguids(source.managedInventory);
  addAaguids(source.aaguidAllowlist);
  addAaguids(source.aaguidWhitelist);
  addAaguids(source.aaguidInventory);
  addAaguids(source.managedAaguidList);

  const formatSource =
    source.enterpriseFormats ??
    source.allowedFormats ??
    source.attestationFormats;
  const enterpriseFormats =
    formatSource === undefined || formatSource === null
      ? null
      : new Set(iterableStrings(formatSource).map((value) => value.toLowerCase()));

  return { rpIds, managedAaguids, enterpriseFormats };
}

function iterableStrings(value: unknown): string[] {
  if (value === undefined || value === null) return [];
  if (typeof value === "string") return [value];
  if (
    typeof value === "object" &&
    value !== null &&
    Symbol.iterator in value
  ) {
    return Array.from(value as Iterable<unknown>).flatMap((entry) =>
      typeof entry === "string" ? [entry] : [],
    );
  }
  return [];
}

function normalizeRpId(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const normalized = value.trim().toLowerCase().replace(/\.$/, "");
  return normalized.length > 0 ? normalized : null;
}

function isEnterpriseRequested(request: RegistrationAttestationRequest): boolean {
  if (!request) return false;
  if (
    request.enterpriseAttestationFormat !== undefined ||
    (request.enterpriseAttestationFormats?.length ?? 0) > 0
  ) {
    return true;
  }
  if (request.enterprise === true || request.enterpriseAttestation === true) {
    return true;
  }

  if (typeof request.attestation === "string") {
    return request.attestation.toLowerCase() === "enterprise";
  }

  if (request.attestation && typeof request.attestation === "object") {
    const value = request.attestation as Record<string, unknown>;
    if (value.enterprise === true) return true;
    if (typeof value.enterprise === "object" && value.enterprise !== null) {
      return true;
    }
  }

  if (
    request.enterpriseAttestation &&
    typeof request.enterpriseAttestation === "object"
  ) {
    return true;
  }

  return false;
}

function collectRequestedFormats(
  request: RegistrationAttestationRequest,
): Set<string> {
  const formats = [
    ...iterableStrings(request.attestationFormats),
    ...iterableStrings(request.requestedAttestationFormats),
    ...iterableStrings(request.enterpriseAttestationFormat),
    ...iterableStrings(request.enterpriseAttestationFormats),
    ...iterableStrings(request.formats),
  ];

  const attestation = request.attestation;
  if (attestation && typeof attestation === "object") {
    const value = attestation as Record<string, unknown>;
    const enterprise = value.enterprise;
    if (typeof enterprise === "object" && enterprise !== null) {
      const nested = enterprise as Record<string, unknown>;
      formats.push(...iterableStrings(nested.format));
      formats.push(...iterableStrings(nested.formats));
    } else {
      formats.push(...iterableStrings(value.format));
      formats.push(...iterableStrings(value.formats));
    }
  }

  const enterprise = request.enterpriseAttestation;
  if (enterprise && typeof enterprise === "object") {
    formats.push(...iterableStrings(enterprise.format));
    formats.push(...iterableStrings(enterprise.formats));
  }

  return new Set(
    formats
      .filter((format): format is string => typeof format === "string")
      .map((format) => format.trim().toLowerCase())
      .filter(Boolean),
  );
}

function parseAttestationResponse(
  response: RegistrationAttestationResponse | undefined,
): ParsedAttestation {
  if (!response) return { fmt: null, attStmt: null, authData: null };

  let objectFmt: string | null = null;
  let objectAttStmt: unknown = null;
  let objectAuthData: unknown = null;

  if (response.attestationObject !== undefined) {
    const objectValue = response.attestationObject;
    if (
      typeof objectValue === "object" &&
      objectValue !== null &&
      !(objectValue instanceof Uint8Array) &&
      !(objectValue instanceof ArrayBuffer)
    ) {
      const parsed = objectValue as Record<string, unknown>;
      objectFmt = valueToText(parsed.fmt);
      objectAttStmt = parsed.attStmt ?? null;
      objectAuthData = parsed.authData ?? null;
    } else {
      const bytes = decodeBinary(objectValue, true);
      if (bytes) {
        const decoded = decodeCbor(bytes);
        if (decoded instanceof Map) {
          objectFmt = valueToText(decoded.get("fmt"));
          objectAttStmt = decoded.get("attStmt") ?? null;
          objectAuthData = decoded.get("authData") ?? null;
        }
      }
    }
  }

  return {
    fmt:
      objectFmt ??
      valueToText(
        response.fmt ??
          response.attestationFormat ??
          getProperty(
            response.attestation ?? response.enterpriseAttestation,
            "fmt",
          ) ??
          getProperty(
            response.attestation ?? response.enterpriseAttestation,
            "format",
          ),
      ),
    attStmt:
      objectAttStmt ??
      response.attStmt ??
      getProperty(response.attestation ?? response.enterpriseAttestation, "attStmt") ??
      null,
    authData:
      objectAuthData ??
      response.authData ??
      getProperty(response.attestation ?? response.enterpriseAttestation, "authData") ??
      null,
  };
}

function extractAuthenticatorAaguid(authData: unknown): Uint8Array | null {
  if (!authData) return null;
  if (authData instanceof Uint8Array) {
    if (authData.byteLength < 53) return null;
    return authData.slice(37, 53);
  }
  if (authData instanceof ArrayBuffer) {
    return extractAuthenticatorAaguid(new Uint8Array(authData));
  }
  if (typeof authData === "string") {
    const bytes = decodeBinary(authData, true);
    return bytes ? extractAuthenticatorAaguid(bytes) : null;
  }
  if (typeof authData === "object") {
    const value = authData as Record<string, unknown>;
    const candidate =
      value.aaguid ??
      value.AAGUID ??
      value.authenticatorAaguid ??
      (typeof value.parsed === "object" && value.parsed !== null
        ? (value.parsed as Record<string, unknown>).aaguid
        : undefined);
    return normalizeAaguidBytes(candidate);
  }
  return null;
}

function extractCertificateInfo(
  response: RegistrationAttestationResponse,
  attStmt: unknown,
): CertificateInfo {
  const empty: CertificateInfo = {
    der: null,
    aaguid: null,
    subjectSerialNumber: null,
    certificateSerialNumber: null,
    storedValue: null,
  };

  const certificate = firstCertificateValue(response, attStmt);
  if (certificate === undefined || certificate === null) return empty;

  if (
    typeof certificate === "object" &&
    !(certificate instanceof Uint8Array) &&
    !(certificate instanceof ArrayBuffer)
  ) {
    const value = certificate as Record<string, unknown>;
    const directAaguid = normalizeAaguidBytes(
      value.aaguid ?? value.AAGUID ?? response.certificateAaguid,
    );
    const directSerial = normalizeSerialNumber(
      value.serialNumber ??
        (typeof value.subject === "object" && value.subject !== null
          ? (value.subject as Record<string, unknown>).serialNumber
          : undefined),
    );
    const derValue = value.der ?? value.raw ?? value.certificate;
    const der = derValue === undefined ? null : decodeBinary(derValue, true);
    if (der) {
      const parsed = parseCertificate(der);
      return {
        der,
        aaguid: parsed?.aaguid ?? directAaguid,
        subjectSerialNumber: parsed?.subjectSerialNumber ?? directSerial,
        certificateSerialNumber: parsed?.certificateSerialNumber ?? null,
        storedValue:
          typeof certificate === "string"
            ? certificate.trim()
            : encodeBase64Url(der),
      };
    }
    if (typeof derValue === "string") {
      return {
        der: null,
        aaguid: directAaguid,
        subjectSerialNumber: directSerial,
        certificateSerialNumber: null,
        storedValue: derValue,
      };
    }
    return {
      ...empty,
      aaguid: directAaguid,
      subjectSerialNumber: directSerial,
      storedValue: directAaguid ? JSON.stringify(certificate) : null,
    };
  }

  const der = decodeBinary(certificate, true);
  if (!der) {
    if (typeof certificate === "string" && certificate.trim().length > 0) {
      return {
        ...empty,
        aaguid: normalizeAaguidBytes(response.certificateAaguid),
        storedValue: certificate,
      };
    }
    return empty;
  }

  const parsed = parseCertificate(der);
  return {
    der,
    aaguid: parsed?.aaguid ?? normalizeAaguidBytes(response.certificateAaguid),
    subjectSerialNumber: parsed?.subjectSerialNumber ?? null,
    certificateSerialNumber: parsed?.certificateSerialNumber ?? null,
    storedValue:
      typeof certificate === "string"
        ? certificate.trim()
        : encodeBase64Url(der),
  };
}

function firstCertificateValue(
  response: RegistrationAttestationResponse,
  attStmt: unknown,
): unknown {
  const fromStatement = getProperty(attStmt, "x5c");
  if (Array.isArray(fromStatement) && fromStatement.length > 0) {
    return fromStatement[0];
  }
  if (Array.isArray(response?.x5c) && response.x5c.length > 0) {
    return response.x5c[0];
  }
  return response?.certificate;
}

function getProperty(value: unknown, key: string): unknown {
  if (!value) return undefined;
  if (value instanceof Map) {
    return value.get(key) ?? value.get(toCborKey(key));
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    return record[key];
  }
  return undefined;
}

function toCborKey(key: string): Uint8Array {
  return new TextEncoder().encode(key);
}

function valueToText(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (value instanceof Uint8Array) {
    return new TextDecoder().decode(value);
  }
  return null;
}

function normalizeAaguidBytes(value: unknown): Uint8Array | null {
  if (value instanceof Uint8Array) {
    return value.byteLength === 16 ? value.slice() : null;
  }
  if (value instanceof ArrayBuffer) {
    return normalizeAaguidBytes(new Uint8Array(value));
  }
  if (typeof value === "string") {
    const normalized = normalizeAaguid(value);
    return normalized ? uuidToBytes(normalized) : null;
  }
  return null;
}

function normalizeAaguid(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const text = value.trim().toLowerCase().replace(/^0x/, "").replace(/[{}]/g, "");
  const compact = text.replace(/-/g, "");
  if (/^[0-9a-f]{32}$/.test(compact)) {
    return `${compact.slice(0, 8)}-${compact.slice(8, 12)}-${compact.slice(
      12,
      16,
    )}-${compact.slice(16, 20)}-${compact.slice(20)}`;
  }
  if (UUID_PATTERN.test(text)) return text;

  const bytes = decodeBase64(text);
  if (bytes && bytes.byteLength === 16) return aaguidToUuid(bytes);
  return null;
}

function aaguidToUuid(bytes: Uint8Array): string {
  const hex = bytesToHex(bytes);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(
    16,
    20,
  )}-${hex.slice(20, 32)}`;
}

function uuidToBytes(uuid: string): Uint8Array {
  const compact = uuid.replace(/-/g, "");
  const bytes = new Uint8Array(16);
  for (let index = 0; index < 16; index += 1) {
    bytes[index] = Number.parseInt(compact.slice(index * 2, index * 2 + 2), 16);
  }
  return bytes;
}

function normalizeSerialNumber(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const normalized = value.trim();
  return normalized.length > 0 ? normalized : null;
}

interface ParsedCertificate {
  aaguid: Uint8Array | null;
  subjectSerialNumber: string | null;
  certificateSerialNumber: string | null;
}

function parseCertificate(der: Uint8Array): ParsedCertificate | null {
  try {
    const certificate = parseDer(der);
    if (!certificate.children || certificate.children.length < 1) return null;
    const tbs = certificate.children[0];
    if (!tbs.children) return null;

    let index = 0;
    if (
      tbs.children[index]?.tagClass === 2 &&
      tbs.children[index]?.tagNumber === 0
    ) {
      index += 1;
    }

    const serialNode = tbs.children[index];
    const certificateSerialNumber = serialNode
      ? normalizeHex(serialNode.bytes)
      : null;
    index += 1;

    const signatureAlgorithm = tbs.children[index];
    const issuer = tbs.children[index + 1];
    const validity = tbs.children[index + 2];
    const subject = tbs.children[index + 3];
    void signatureAlgorithm;
    void issuer;
    void validity;

    const subjectSerialNumber = subject
      ? findSubjectSerialNumber(subject, SUBJECT_SERIAL_NUMBER_OID)
      : null;

    let aaguid: Uint8Array | null = null;
    const extensions = tbs.children.find(
      (node) => node.tagClass === 2 && node.tagNumber === 3,
    );
    if (extensions?.children?.[0]) {
      aaguid = findAaguidExtension(extensions.children[0]);
    }

    return { aaguid, subjectSerialNumber, certificateSerialNumber };
  } catch {
    return null;
  }
}

function findSubjectSerialNumber(
  node: DerNode,
  oid: string,
): string | null {
  if (!node.children) return null;
  if (
    node.children[0]?.tagClass === 0 &&
    node.children[0]?.tagNumber === 6 &&
    decodeOid(node.children[0].bytes) === oid
  ) {
    const valueNode = node.children[1];
    if (valueNode) return decodeAsn1String(valueNode);
  }
  for (const child of node.children) {
    const found = findSubjectSerialNumber(child, oid);
    if (found) return found;
  }
  return null;
}

function findAaguidExtension(extensions: DerNode): Uint8Array | null {
  if (!extensions.children) return null;
  for (const extension of extensions.children) {
    if (!extension.children || extension.children.length < 2) continue;
    const oidNode = extension.children[0];
    if (oidNode.tagClass !== 0 || oidNode.tagNumber !== 6) continue;
    if (decodeOid(oidNode.bytes) !== AAGUID_EXTENSION_OID) continue;
    const valueNode = extension.children[extension.children.length - 1];
    if (valueNode.tagClass === 0 && valueNode.tagNumber === 4) {
      return extractAaguidBytes(valueNode.bytes);
    }
  }
  return null;
}

function extractAaguidBytes(value: Uint8Array): Uint8Array | null {
  if (value.byteLength === 16) return value.slice();
  try {
    const node = parseDer(value);
    const direct = findSixteenByteOctetString(node);
    if (direct) return direct;
  } catch {
    return null;
  }
  return null;
}

function findSixteenByteOctetString(
  node: DerNode,
): Uint8Array | null {
  if (
    node.tagClass === 0 &&
    node.tagNumber === 4 &&
    node.bytes.byteLength === 16
  ) {
    return node.bytes.slice();
  }
  if (node.children) {
    for (const child of node.children) {
      const found = findSixteenByteOctetString(child);
      if (found) return found;
    }
  }
  return null;
}

function decodeAsn1String(node: DerNode): string | null {
  const bytes = node.bytes;
  if (bytes.byteLength === 0) return null;
  if (node.tagNumber === 30) {
    let result = "";
    for (let index = 0; index + 1 < bytes.byteLength; index += 2) {
      result += String.fromCharCode(
        (bytes[index] << 8) | bytes[index + 1],
      );
    }
    return result;
  }
  return new TextDecoder().decode(bytes);
}

function decodeOid(bytes: Uint8Array): string {
  if (bytes.byteLength === 0) return "";
  const first = bytes[0];
  const values = [Math.min(Math.floor(first / 40), 2), first % 40];
  let current = 0;
  for (let index = 1; index < bytes.byteLength; index += 1) {
    const byte = bytes[index];
    current = (current << 7) | (byte & 0x7f);
    if ((byte & 0x80) === 0) {
      values.push(current);
      current = 0;
    }
  }
  return values.join(".");
}

function normalizeHex(bytes: Uint8Array): string {
  let start = 0;
  while (start < bytes.byteLength - 1 && bytes[start] === 0) start += 1;
  return bytesToHex(bytes.slice(start));
}

function bytesToHex(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("hex");
}

interface DerNode {
  tagClass: number;
  constructed: boolean;
  tagNumber: number;
  bytes: Uint8Array;
  children: DerNode[] | null;
  fragment: () => Uint8Array;
}

function parseDer(data: Uint8Array): DerNode {
  const state = { offset: 0 };
  const node = readDerNode(data, state);
  return node;
}

function readDerNode(data: Uint8Array, state: { offset: number }): DerNode {
  const start = state.offset;
  if (start >= data.byteLength) throw new Error("Unexpected end of DER");
  const tagByte = data[state.offset];
  state.offset += 1;
  const tagClass = tagByte >> 6;
  const constructed = (tagByte & 0x20) !== 0;
  let tagNumber = tagByte & 0x1f;
  if (tagNumber === 0x1f) {
    tagNumber = 0;
    let byte = 0;
    do {
      if (state.offset >= data.byteLength) throw new Error("Invalid DER tag");
      byte = data[state.offset];
      state.offset += 1;
      tagNumber = (tagNumber << 7) | (byte & 0x7f);
    } while ((byte & 0x80) !== 0);
  }

  if (state.offset >= data.byteLength) throw new Error("Missing DER length");
  const firstLength = data[state.offset];
  state.offset += 1;
  let length = 0;
  if (firstLength < 0x80) {
    length = firstLength;
  } else {
    const lengthBytes = firstLength & 0x7f;
    if (lengthBytes === 0 || lengthBytes > 4) {
      throw new Error("Unsupported DER length");
    }
    for (let index = 0; index < lengthBytes; index += 1) {
      if (state.offset >= data.byteLength) throw new Error("Invalid DER length");
      length = length * 256 + data[state.offset];
      state.offset += 1;
    }
  }

  const contentStart = state.offset;
  const end = contentStart + length;
  if (end > data.byteLength) throw new Error("DER value exceeds buffer");
  state.offset = end;
  const content = data.slice(contentStart, end);

  let children: DerNode[] | null = null;
  if (constructed) {
    children = [];
    const childState = { offset: 0 };
    while (childState.offset < content.byteLength) {
      children.push(readDerNode(content, childState));
    }
  }

  return {
    tagClass,
    constructed,
    tagNumber,
    bytes: content,
    children,
    fragment: () => data.slice(start, end),
  };
}

export function parseAttestationObject(
  value: string | Uint8Array | ArrayBuffer,
): {
  fmt: string | null;
  attStmt: unknown;
  authData: unknown;
} {
  const bytes = decodeBinary(value, true);
  if (!bytes) throw new TypeError("Invalid attestation object");
  const decoded = decodeCbor(bytes);
  if (!(decoded instanceof Map)) {
    throw new TypeError("Attestation object is not a CBOR map");
  }
  return {
    fmt: valueToText(decoded.get("fmt")),
    attStmt: decoded.get("attStmt") ?? null,
    authData: decoded.get("authData") ?? null,
  };
}

export function extractCertificateMetadata(
  value: string | Uint8Array | ArrayBuffer,
): {
  aaguid: string | null;
  subjectSerialNumber: string | null;
  certificateSerialNumber: string | null;
} | null {
  const bytes = decodeBinary(value, true);
  if (!bytes) return null;
  const parsed = parseCertificate(bytes);
  if (!parsed) return null;
  return {
    aaguid: parsed.aaguid ? aaguidToUuid(parsed.aaguid) : null,
    subjectSerialNumber: parsed.subjectSerialNumber,
    certificateSerialNumber: parsed.certificateSerialNumber,
  };
}

type CborValue =
  | number
  | string
  | boolean
  | null
  | Uint8Array
  | CborValue[]
  | Map<unknown, CborValue>;

function decodeCbor(data: Uint8Array): CborValue {
  const state = { offset: 0 };
  const value = readCborValue(data, state);
  if (state.offset !== data.byteLength) {
    throw new TypeError("Unexpected data after CBOR value");
  }
  return value;
}

function readCborValue(
  data: Uint8Array,
  state: { offset: number },
): CborValue {
  if (state.offset >= data.byteLength) throw new TypeError("Unexpected CBOR end");
  const initial = data[state.offset];
  state.offset += 1;
  const major = initial >> 5;
  const additional = initial & 0x1f;

  if (major === 7) {
    if (additional === 20) return false;
    if (additional === 21) return true;
    if (additional === 22 || additional === 23) return null;
    if (additional === 25) return readFloat16(data, state);
    if (additional === 26) return readFloat32(data, state);
    if (additional === 27) return readFloat64(data, state);
    throw new TypeError("Unsupported CBOR simple value");
  }

  if (major === 6) {
    readCborValue(data, state);
    return readCborValue(data, state);
  }

  const length = readCborLength(data, state, additional);

  if (major === 0) return length;
  if (major === 1) return -1 - length;

  if (major === 2) {
    if (length === -1) {
      const chunks: Uint8Array[] = [];
      let total = 0;
      while (data[state.offset] !== 0xff) {
        const chunk = readCborValue(data, state);
        if (!(chunk instanceof Uint8Array)) {
          throw new TypeError("Invalid CBOR byte string chunk");
        }
        chunks.push(chunk);
        total += chunk.byteLength;
      }
      state.offset += 1;
      const result = new Uint8Array(total);
      let offset = 0;
      for (const chunk of chunks) {
        result.set(chunk, offset);
        offset += chunk.byteLength;
      }
      return result;
    }
    return readBytes(data, state, length);
  }

  if (major === 3) {
    if (length === -1) {
      let result = "";
      while (data[state.offset] !== 0xff) {
        const chunk = readCborValue(data, state);
        if (typeof chunk !== "string") {
          throw new TypeError("Invalid CBOR text string chunk");
        }
        result += chunk;
      }
      state.offset += 1;
      return result;
    }
    return new TextDecoder().decode(readBytes(data, state, length));
  }

  if (major === 4) {
    const values: CborValue[] = [];
    if (length === -1) {
      while (data[state.offset] !== 0xff) {
        values.push(readCborValue(data, state));
      }
      state.offset += 1;
      return values;
    }
    for (let index = 0; index < length; index += 1) {
      values.push(readCborValue(data, state));
    }
    return values;
  }

  if (major === 5) {
    const result = new Map<unknown, CborValue>();
    if (length === -1) {
      while (data[state.offset] !== 0xff) {
        const key = readCborValue(data, state);
        const value = readCborValue(data, state);
        result.set(key, value);
      }
      state.offset += 1;
      return result;
    }
    for (let index = 0; index < length; index += 1) {
      const key = readCborValue(data, state);
      result.set(key, readCborValue(data, state));
    }
    return result;
  }

  throw new TypeError("Unsupported CBOR major type");
}

function readCborLength(
  data: Uint8Array,
  state: { offset: number },
  additional: number,
): number {
  if (additional < 24) return additional;
  if (additional === 31) return -1;
  const byteCount =
    additional === 24 ? 1 : additional === 25 ? 2 : additional === 26 ? 4 : additional === 27 ? 8 : 0;
  if (byteCount === 0) throw new TypeError("Invalid CBOR length");
  let value = 0;
  for (let index = 0; index < byteCount; index += 1) {
    if (state.offset >= data.byteLength) throw new TypeError("Invalid CBOR length");
    value = value * 256 + data[state.offset];
    state.offset += 1;
  }
  return value;
}

function readBytes(
  data: Uint8Array,
  state: { offset: number },
  length: number,
): Uint8Array {
  const end = state.offset + length;
  if (end > data.byteLength) throw new TypeError("CBOR value exceeds buffer");
  const bytes = data.slice(state.offset, end);
  state.offset = end;
  return bytes;
}

function readFloat16(
  data: Uint8Array,
  state: { offset: number },
): number {
  if (state.offset + 2 > data.byteLength) throw new TypeError("Invalid CBOR float");
  const view = new DataView(data.buffer, data.byteOffset + state.offset, 2);
  state.offset += 2;
  const half = view.getUint16(0);
  const sign = half & 0x8000 ? -1 : 1;
  const exponent = (half >> 10) & 0x1f;
  const fraction = half & 0x3ff;
  if (exponent === 0) return sign * Math.pow(2, -14) * (fraction / 1024);
  if (exponent === 0x1f) {
    return fraction === 0 ? sign * Infinity : Number.NaN;
  }
  return sign * Math.pow(2, exponent - 15) * (1 + fraction / 1024);
}

function readFloat32(
  data: Uint8Array,
  state: { offset: number },
): number {
  if (state.offset + 4 > data.byteLength) throw new TypeError("Invalid CBOR float");
  const view = new DataView(data.buffer, data.byteOffset + state.offset, 4);
  const value = view.getFloat32(0);
  state.offset += 4;
  return value;
}

function readFloat64(
  data: Uint8Array,
  state: { offset: number },
): number {
  if (state.offset + 8 > data.byteLength) throw new TypeError("Invalid CBOR float");
  const view = new DataView(data.buffer, data.byteOffset + state.offset, 8);
  const value = view.getFloat64(0);
  state.offset += 8;
  return value;
}

function decodeBinary(
  value: unknown,
  allowBase64: boolean,
): Uint8Array | null {
  if (value instanceof Uint8Array) return value.slice();
  if (value instanceof ArrayBuffer) return new Uint8Array(value.slice(0));
  if (ArrayBuffer.isView(value)) {
    return new Uint8Array(
      value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength),
    );
  }
  if (typeof value !== "string" || !allowBase64) return null;

  const text = value.trim();
  if (text.length === 0) return null;
  const pem = text.match(
    /-----BEGIN [^-]+-----([\s\S]*?)-----END [^-]+-----/,
  );
  if (pem) {
    const base64 = pem[1].replace(/\s+/g, "");
    return decodeBase64(base64);
  }
  return decodeBase64(text);
}

function decodeBase64(value: string): Uint8Array | null {
  const compact = value.replace(/\s+/g, "");
  if (!compact) return null;
  if (!/^[A-Za-z0-9+/_=-]+$/.test(compact)) return null;
  const normalized = compact
    .replace(/-/g, "+")
    .replace(/_/g, "/")
    .replace(/=+$/, "");
  const padded = normalized.padEnd(
    normalized.length + ((4 - (normalized.length % 4)) % 4),
    "=",
  );
  try {
    return new Uint8Array(Buffer.from(padded, "base64"));
  } catch {
    return null;
  }
}

function encodeBase64Url(value: Uint8Array): string {
  return Buffer.from(value).toString("base64url");
}
