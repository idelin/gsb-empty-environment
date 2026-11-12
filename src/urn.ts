/**
 * Validation for UPnP URNs used in serviceType / deviceType fields and as
 * SOAPAction values. Restricting these to the documented shape prevents
 * header/XML injection via an attacker-controlled descriptor.
 */

const URN_PATTERN =
  /^urn:[a-z0-9][a-z0-9-]{0,31}:[A-Za-z0-9][A-Za-z0-9_.-]*:[A-Za-z0-9][A-Za-z0-9_.-]*:[0-9]+$/;

/** Accepts strings shaped like urn:schemas-upnp-org:service:AVTransport:1. */
export function isUpnpUrn(value: string): boolean {
  return value.length <= 256 && URN_PATTERN.test(value);
}
