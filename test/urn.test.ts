import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { isUpnpUrn } from "../src/urn.ts";

describe("isUpnpUrn", () => {
  it("accepts documented UPnP service/device URNs", () => {
    assert.equal(
      isUpnpUrn("urn:schemas-upnp-org:service:AVTransport:1"),
      true,
    );
    assert.equal(
      isUpnpUrn("urn:schemas-upnp-org:device:MediaRenderer:1"),
      true,
    );
    assert.equal(
      isUpnpUrn("urn:microsoft-com:service:X_MS_MediaReceiverRegistrar:1"),
      true,
    );
  });

  it("rejects injection payloads and malformed URNs", () => {
    for (const value of [
      "urn:schemas-upnp-org:service:AVTransport:1\r\nX-Inject: 1",
      'urn:schemas-upnp-org:service:AVTransport:1"evil',
      "http://schemas-upnp-org/service/AVTransport",
      "service:AVTransport:1",
      "urn:schemas-upnp-org:service::1",
      "",
    ]) {
      assert.equal(isUpnpUrn(value), false, value);
    }
  });
});
