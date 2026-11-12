/**
 * Device descriptor parsing and validation.
 *
 * After the XML bytes have been fetched with the host pinned to the SSDP
 * source address, this module turns the document into a typed descriptor
 * whose every absolute reference (URLBase, controlURL) has been re-checked
 * against that same host.
 */

import {
  MalformedServiceError,
  XmlTooLargeError,
} from "./errors.ts";
import {
  childText,
  findAll,
  findChild,
  parseXml,
  type XmlElement,
} from "./xml.ts";
import {
  formatHttpUrl,
  resolveControlUrl,
  validateLocation,
  validateUrlBase,
  type ParsedHttpUrl,
} from "./url-policy.ts";
import type { FetchResponse, HttpFetcher } from "./http-fetch.ts";
import { NodeHttpFetcher } from "./http-fetch.ts";
import { isUpnpUrn } from "./urn.ts";

export interface DeviceService {
  /** serviceType URN declared by the device, e.g. urn:schemas-upnp-org:service:AVTransport:1. */
  serviceType: string;
  serviceId: string;
  /** Fully validated absolute control endpoint, host-pinned to the SSDP source. */
  controlUrl: string;
  eventSubUrl: string | undefined;
  scpdUrl: string | undefined;
}

export interface DeviceDescriptor {
  /** Host every URL in the descriptor was pinned to. */
  anchorHost: string;
  /** Final description URL after validated redirects. */
  descriptionUrl: string;
  deviceType: string | undefined;
  friendlyName: string | undefined;
  manufacturer: string | undefined;
  modelName: string | undefined;
  udn: string | undefined;
  /** Services of this device and all embedded devices. */
  services: DeviceService[];
  /** Raw parsed root element for callers that need additional fields. */
  raw: XmlElement;
}

export interface FetchDescriptorOptions {
  /** Maximum accepted description size. Defaults to 64 KiB. */
  maxXmlBytes?: number;
  maxRedirects?: number;
  timeoutMs?: number;
  /** Override the transport (used by tests). */
  fetcher?: HttpFetcher;
  headers?: Record<string, string>;
}

export const DEFAULT_MAX_XML_BYTES = 64 * 1024;

/**
 * Entry point for the device-discovery flow.
 *
 * @param location LOCATION header received in the SSDP response.
 * @param ssdpSource Source IP of the SSDP datagram (socket remote address).
 */
export async function fetchDeviceDescription(
  location: string,
  ssdpSource: string,
  options: FetchDescriptorOptions = {},
): Promise<DeviceDescriptor> {
  const maxXmlBytes = options.maxXmlBytes ?? DEFAULT_MAX_XML_BYTES;
  if (!Number.isInteger(maxXmlBytes) || maxXmlBytes <= 0) {
    throw new XmlTooLargeError("maxXmlBytes must be a positive integer");
  }

  const initial = validateLocation(location, ssdpSource);
  const fetcher = options.fetcher ?? new NodeHttpFetcher();
  const response: FetchResponse = await fetcher.get(initial, initial.host, {
    maxBytes: maxXmlBytes,
    maxRedirects: options.maxRedirects ?? 5,
    timeoutMs: options.timeoutMs ?? 5000,
    headers: options.headers,
  });

  if (response.body.length > maxXmlBytes) {
    throw new XmlTooLargeError(
      `Description of ${response.body.length} bytes exceeds limit of ${maxXmlBytes}`,
    );
  }

  const xmlText = response.body.toString("utf8");
  const root = parseXml(xmlText);
  if (root.tag !== "root") {
    throw new MalformedServiceError(
      `Device descriptor root element must be <root>, got <${root.tag}>`,
    );
  }
  const device = findChild(root, "device");
  if (device === undefined) {
    throw new MalformedServiceError(
      "Device descriptor is missing the <device> element",
    );
  }

  const anchorHost = initial.host;
  const declaredUrlBase = childText(root, "URLBase");
  const urlBase =
    validateUrlBase(declaredUrlBase, anchorHost) ??
    formatHttpUrl(initial);

  const services = collectServices(device, urlBase, anchorHost);

  return {
    anchorHost,
    descriptionUrl: response.url,
    deviceType: childText(device, "deviceType"),
    friendlyName: childText(device, "friendlyName"),
    manufacturer: childText(device, "manufacturer"),
    modelName: childText(device, "modelName"),
    udn: childText(device, "UDN"),
    services,
    raw: root,
  };
}

function collectServices(
  device: XmlElement,
  urlBase: string,
  anchorHost: string,
): DeviceService[] {
  const serviceElements = findAll(device, "service");
  const services: DeviceService[] = [];

  for (const service of serviceElements) {
    const serviceType = childText(service, "serviceType");
    const serviceId = childText(service, "serviceId");
    const controlUrlText = childText(service, "controlURL");

    if (serviceType === undefined || serviceId === undefined) {
      throw new MalformedServiceError(
        "Each <service> must declare serviceType and serviceId",
      );
    }
    if (!isUpnpUrn(serviceType)) {
      throw new MalformedServiceError(
        `serviceType is not a valid UPnP URN: ${serviceType}`,
      );
    }
    if (controlUrlText === undefined) {
      throw new MalformedServiceError(
        `Service ${serviceType} is missing controlURL`,
      );
    }

    const resolved: ParsedHttpUrl = resolveControlUrl(
      controlUrlText,
      urlBase,
      anchorHost,
    );

    services.push({
      serviceType,
      serviceId,
      controlUrl: formatHttpUrl(resolved),
      eventSubUrl: optionalUrl(
        childText(service, "eventSubURL"),
        urlBase,
        anchorHost,
      ),
      scpdUrl: optionalUrl(
        childText(service, "SCPDURL"),
        urlBase,
        anchorHost,
      ),
    });
  }

  return services;
}

function optionalUrl(
  value: string | undefined,
  urlBase: string,
  anchorHost: string,
): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  return formatHttpUrl(resolveControlUrl(value, urlBase, anchorHost));
}
