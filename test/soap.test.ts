import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { buildSoapRequest, findService } from "../src/soap.ts";
import { fetchDeviceDescription } from "../src/descriptor.ts";
import { MockHttpFetcher } from "../mocks/mock-http-fetcher.ts";
import { ServiceNotDeclaredError } from "../src/errors.ts";

const SOURCE = "192.168.1.10";

const DESCRIPTOR_XML = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Renderer</friendlyName>
    <UDN>uuid:1</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <controlURL>/ctl/AVTransport</controlURL>
      </service>
    </serviceList>
  </device>
</root>`;

async function loadDescriptor() {
  const fetcher = new MockHttpFetcher({
    "/rootDesc.xml": { body: DESCRIPTOR_XML },
  });
  return fetchDeviceDescription(`http://${SOURCE}/rootDesc.xml`, SOURCE, {
    fetcher,
  });
}

describe("SOAP request building", () => {
  it("targets only a declared serviceType and derives SOAPAction from it", async () => {
    const descriptor = await loadDescriptor();
    const request = buildSoapRequest(
      descriptor,
      "urn:schemas-upnp-org:service:AVTransport:1",
      "SetAVTransportURI",
      { InstanceID: 0, CurrentURI: "http://192.168.1.10/media.mp3" },
    );

    assert.equal(request.method, "POST");
    assert.equal(request.url, `http://${SOURCE}/ctl/AVTransport`);
    assert.equal(
      request.headers.SOAPAction,
      '"urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI"',
    );
    assert.match(request.body, /xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"/);
    assert.match(request.body, /<CurrentURI>http:\/\/192\.168\.1\.10\/media\.mp3<\/CurrentURI>/);
    assert.equal(
      request.headers["Content-Length"],
      String(Buffer.byteLength(request.body, "utf8")),
    );
  });

  it("rejects service types that the device never declared", async () => {
    const descriptor = await loadDescriptor();
    assert.throws(
      () =>
        buildSoapRequest(
          descriptor,
          "urn:schemas-upnp-org:service:WANIPConnection:1",
          "GetExternalIPAddress",
        ),
      ServiceNotDeclaredError,
    );
    assert.throws(
      () => findService(descriptor, "urn:schemas-upnp-org:service:AVTransport:2"),
      ServiceNotDeclaredError,
    );
  });

  it("XML-escapes argument values so they cannot inject elements", async () => {
    const descriptor = await loadDescriptor();
    const request = buildSoapRequest(
      descriptor,
      "urn:schemas-upnp-org:service:AVTransport:1",
      "Play",
      { Speed: "1</u:Play><evil/>&" },
    );
    assert.match(request.body, /&lt;evil\/&gt;&amp;/);
    assert.doesNotMatch(request.body, /<evil\/>/);
  });

  it("rejects malformed action names", async () => {
    const descriptor = await loadDescriptor();
    assert.throws(
      () =>
        buildSoapRequest(
          descriptor,
          "urn:schemas-upnp-org:service:AVTransport:1",
          "Bad Action",
        ),
      ServiceNotDeclaredError,
    );
  });

  it("rejects malformed argument element names", async () => {
    const descriptor = await loadDescriptor();
    assert.throws(
      () =>
        buildSoapRequest(
          descriptor,
          "urn:schemas-upnp-org:service:AVTransport:1",
          "Play",
          { "x><evil": 1 },
        ),
      ServiceNotDeclaredError,
    );
  });
});
