/**
 * SOAP control-request construction for services advertised in a validated
 * device descriptor.
 *
 * The attack surface here is a descriptor that smuggles an absolute
 * controlURL pointing at another internal host, or a caller that invokes an
 * arbitrary SOAPAction. Both are constrained:
 *
 *  - the target service must be one declared by the descriptor, and its
 *    controlURL has already been host-pinned during descriptor parsing;
 *  - the SOAPAction header is always the declared serviceType URN plus the
 *    requested action, never a free-form caller value;
 *  - argument values are XML-escaped and the envelope uses the fixed
 *    UPnP SOAP namespaces.
 */

import { Buffer } from "node:buffer";

import { ServiceNotDeclaredError } from "./errors.ts";
import type { DeviceDescriptor, DeviceService } from "./descriptor.ts";
import { formatHttpUrl, parseHttpUrl } from "./url-policy.ts";
import { isUpnpUrn } from "./urn.ts";

export interface SoapRequest {
  url: string;
  method: "POST";
  headers: Record<string, string>;
  body: string;
}

const SOAP_ENVELOPE_NS = "http://schemas.xmlsoap.org/soap/envelope/";

function escapeXml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

/**
 * Finds a service by its declared serviceType URN. Lookup is exact: a
 * prefix/suffix match could be used to reach a sibling service.
 */
export function findService(
  descriptor: DeviceDescriptor,
  serviceType: string,
): DeviceService {
  const match = descriptor.services.find(
    (service) => service.serviceType === serviceType,
  );
  if (match === undefined) {
    throw new ServiceNotDeclaredError(
      `Service type ${serviceType} is not declared by device ` +
        `${descriptor.udn ?? descriptor.anchorHost}`,
    );
  }
  return match;
}

/**
 * Builds a UPnP SOAP request for one action of a declared service.
 *
 * @param descriptor Result of {@link fetchDeviceDescription}.
 * @param serviceType Exact serviceType URN from the descriptor.
 * @param action SOAP action name (the element inside the service namespace).
 * @param arguments_ Named input arguments; values are XML-escaped.
 */
export function buildSoapRequest(
  descriptor: DeviceDescriptor,
  serviceType: string,
  action: string,
  arguments_: Record<string, string | number | boolean> = {},
): SoapRequest {
  if (!/^[A-Za-z][\w-]*$/.test(action)) {
    throw new ServiceNotDeclaredError(
      `Invalid SOAP action name: ${JSON.stringify(action)}`,
    );
  }
  if (!isUpnpUrn(serviceType)) {
    throw new ServiceNotDeclaredError(
      `serviceType is not a valid UPnP URN: ${serviceType}`,
    );
  }

  const service = findService(descriptor, serviceType);

  // Re-validate at use time: descriptors may be cached and the controlURL
  // must still resolve to the pinned private host.
  const parsedControl = parseHttpUrl(service.controlUrl);
  if (parsedControl.host !== descriptor.anchorHost) {
    throw new ServiceNotDeclaredError(
      `controlURL host ${parsedControl.host} no longer matches anchor ` +
        descriptor.anchorHost,
    );
  }

  const argumentXml = Object.entries(arguments_)
    .map(([name, value]) => {
      if (!/^[A-Za-z_][\w.-]*$/.test(name)) {
        throw new ServiceNotDeclaredError(
          `Invalid SOAP argument name: ${JSON.stringify(name)}`,
        );
      }
      return `    <${name}>${escapeXml(String(value))}</${name}>`;
    })
    .join("\n");
  const body =
    `<?xml version="1.0" encoding="utf-8"?>\n` +
    `<s:Envelope xmlns:s="${SOAP_ENVELOPE_NS}" ` +
    `s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">\n` +
    `  <s:Body>\n` +
    `    <u:${escapeXml(action)} xmlns:u="${escapeXml(service.serviceType)}">\n` +
    (argumentXml === "" ? "" : `${argumentXml}\n`) +
    `    </u:${escapeXml(action)}>\n` +
    `  </s:Body>\n` +
    `</s:Envelope>\n`;

  return {
    url: formatHttpUrl(parsedControl),
    method: "POST",
    headers: {
      "Content-Type": `text/xml; charset="utf-8"`,
      SOAPAction: `"${service.serviceType}#${action}"`,
      "Content-Length": String(Buffer.byteLength(body, "utf8")),
      Connection: "close",
    },
    body,
  };
}
