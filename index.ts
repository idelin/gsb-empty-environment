/**
 * SSDP device-description fetching package for the media control point.
 *
 * Security guarantees (see src/* for the enforcement points):
 *  - LOCATION host must equal the SSDP datagram source and be a private
 *    RFC1918 / RFC4193 address, plain http on an explicit/port-80 port;
 *  - every redirect hop must stay on that exact host;
 *  - URLBase and every controlURL resolve back to that exact host;
 *  - descriptors are size-capped while streaming and parsed without DTDs,
 *    external entities or custom entity expansion;
 *  - SOAPAction is derived solely from serviceTypes declared in the
 *    descriptor.
 */

export {
  fetchDeviceDescription,
  DEFAULT_MAX_XML_BYTES,
  type DeviceDescriptor,
  type DeviceService,
  type FetchDescriptorOptions,
} from "./src/descriptor.ts";

export {
  buildSoapRequest,
  findService,
  type SoapRequest,
} from "./src/soap.ts";

export {
  parseHttpUrl,
  validateLocation,
  validateRedirect,
  validateUrlBase,
  resolveControlUrl,
  formatHttpUrl,
  type ParsedHttpUrl,
} from "./src/url-policy.ts";

export {
  isPrivateAddress,
  isIpLiteral,
  sameHost,
  normalizeHost,
} from "./src/network.ts";

export { parseXml, type XmlElement } from "./src/xml.ts";

export { isUpnpUrn } from "./src/urn.ts";

export {
  NodeHttpFetcher,
  type HttpFetcher,
  type FetchOptions,
  type FetchResponse,
} from "./src/http-fetch.ts";

export {
  DescriptionFetchError,
  InvalidLocationError,
  HostMismatchError,
  NonPrivateHostError,
  UnsafeSchemeError,
  UnsafePortError,
  RedirectHostMismatchError,
  TooManyRedirectsError,
  BadResponseError,
  XmlTooLargeError,
  UnsafeXmlError,
  MalformedXmlError,
  UrlBaseHostMismatchError,
  ControlUrlHostMismatchError,
  MalformedServiceError,
  ServiceNotDeclaredError,
  HttpStatusError,
  type FetchErrorCode,
} from "./src/errors.ts";
