import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";

import { fetchDeviceDescription } from "../src/descriptor.ts";
import { MockHttpFetcher } from "../mocks/mock-http-fetcher.ts";
import {
  ControlUrlHostMismatchError,
  RedirectHostMismatchError,
  UnsafeXmlError,
  UrlBaseHostMismatchError,
} from "../src/errors.ts";

const SOURCE = "192.168.1.10";
const LOCATION = `http://${SOURCE}:5000/rootDesc.xml`;

const VALID_DESCRIPTOR = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>1</minor></specVersion>
  <URLBase>http://192.168.1.10:5000/</URLBase>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>Living Room Renderer</friendlyName>
    <manufacturer>Acme</manufacturer>
    <modelName>MR-100</modelName>
    <UDN>uuid:abcd-1234</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <controlURL>/ctl/AVTransport</controlURL>
        <eventSubURL>/evt/AVTransport</eventSubURL>
        <SCPDURL>/scpd/AVTransport.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
        <controlURL>ctl/RenderingControl</controlURL>
        <eventSubURL></eventSubURL>
        <SCPDURL>scpd/RenderingControl.xml</SCPDURL>
      </service>
    </serviceList>
    <deviceList>
      <device>
        <friendlyName>Embedded</friendlyName>
        <serviceList>
          <service>
            <serviceType>urn:schemas-upnp-org:service:Embedded:1</serviceType>
            <serviceId>urn:upnp-org:serviceId:Embedded</serviceId>
            <controlURL>/ctl/Embedded</controlURL>
            <SCPDURL>/scpd/Embedded.xml</SCPDURL>
          </service>
        </serviceList>
      </device>
    </deviceList>
  </device>
</root>
`;

describe("fetchDeviceDescription", () => {
  it("accepts a legitimate device and pins every URL to the SSDP source", async () => {
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: VALID_DESCRIPTOR },
    });
    const descriptor = await fetchDeviceDescription(LOCATION, SOURCE, {
      fetcher,
    });

    assert.equal(descriptor.anchorHost, SOURCE);
    assert.equal(descriptor.friendlyName, "Living Room Renderer");
    assert.equal(descriptor.udn, "uuid:abcd-1234");
    assert.equal(descriptor.services.length, 3);

    const avTransport = descriptor.services.find(
      (service) =>
        service.serviceType === "urn:schemas-upnp-org:service:AVTransport:1",
    )!;
    assert.equal(
      avTransport.controlUrl,
      "http://192.168.1.10:5000/ctl/AVTransport",
    );
    // Relative controlURL is resolved against URLBase.
    const rendering = descriptor.services.find(
      (service) =>
        service.serviceType ===
        "urn:schemas-upnp-org:service:RenderingControl:1",
    )!;
    assert.equal(
      rendering.controlUrl,
      "http://192.168.1.10:5000/ctl/RenderingControl",
    );
    // Embedded-device services are collected recursively.
    assert.ok(
      descriptor.services.some(
        (service) =>
          service.serviceType === "urn:schemas-upnp-org:service:Embedded:1",
      ),
    );
    for (const service of descriptor.services) {
      assert.match(service.controlUrl, /^http:\/\/192\.168\.1\.10(:5000)?\//);
    }
    assert.equal(fetcher.requests[0].host, SOURCE);
  });

  it("follows same-host redirects and keeps the anchor host", async () => {
    const fetcher = new MockHttpFetcher({
      "/old.xml": {
        status: 302,
        headers: { location: "http://192.168.1.10:5000/rootDesc.xml" },
      },
      "/rootDesc.xml": { body: VALID_DESCRIPTOR },
    });
    const descriptor = await fetchDeviceDescription(
      "http://192.168.1.10:5000/old.xml",
      SOURCE,
      { fetcher },
    );
    assert.equal(fetcher.requests.length, 2);
    assert.equal(fetcher.requests[1].path, "/rootDesc.xml");
    assert.equal(descriptor.anchorHost, SOURCE);
  });

  it("rejects a redirect that switches to another internal host", async () => {
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": {
        status: 302,
        headers: { location: "http://192.168.1.20:49152/evil.xml" },
      },
      "/evil.xml": { body: VALID_DESCRIPTOR },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      RedirectHostMismatchError,
    );
    // The second host must never have been contacted.
    assert.equal(
      fetcher.requests.some((request) => request.host === "192.168.1.20"),
      false,
    );
  });

  it("rejects a redirect chain that changes host on the second hop", async () => {
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": {
        status: 301,
        headers: { location: "/hop2.xml" },
      },
      "/hop2.xml": {
        status: 302,
        headers: { location: "http://10.0.0.99/x.xml" },
      },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      RedirectHostMismatchError,
    );
    assert.equal(
      fetcher.requests.some((request) => request.host === "10.0.0.99"),
      false,
    );
  });

  it("caps the number of redirect hops", async () => {
    const fetcher = new MockHttpFetcher({
      "/r0": { status: 302, headers: { location: "/r1" } },
      "/r1": { status: 302, headers: { location: "/r2" } },
      "/r2": { status: 302, headers: { location: "/r3" } },
      "/r3": { status: 302, headers: { location: "/r4" } },
      "/r4": { status: 302, headers: { location: "/r5" } },
      "/r5": { status: 302, headers: { location: "/r6" } },
    });
    await assert.rejects(
      () =>
        fetchDeviceDescription(
          "http://192.168.1.10:5000/r0",
          SOURCE,
          { fetcher, maxRedirects: 3 },
        ),
      /redirect/i,
    );
  });

  it("rejects an absolute controlURL pointing at another internal address", async () => {
    const malicious = VALID_DESCRIPTOR.replace(
      "<controlURL>/ctl/AVTransport</controlURL>",
      "<controlURL>http://192.168.1.20:49152/ctl/AVTransport</controlURL>",
    );
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: malicious },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      ControlUrlHostMismatchError,
    );
  });

  it("rejects a URLBase pointing at another internal address", async () => {
    const malicious = VALID_DESCRIPTOR.replace(
      "<URLBase>http://192.168.1.10:5000/</URLBase>",
      "<URLBase>http://10.0.0.99:9000/</URLBase>",
    );
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: malicious },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      UrlBaseHostMismatchError,
    );
  });

  it("rejects oversized XML before it can be parsed", async () => {
    const oversized = Buffer.alloc(70 * 1024, 0x61);
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: oversized },
    });
    await assert.rejects(
      () =>
        fetchDeviceDescription(LOCATION, SOURCE, {
          fetcher,
          maxXmlBytes: 64 * 1024,
        }),
      /exceeds limit/,
    );
  });

  it("rejects XML carrying a DOCTYPE / external entity", async () => {
    const xxe =
      `<!DOCTYPE root [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
      `<root><device><service>` +
      `<serviceType>x:y</serviceType><serviceId>i</serviceId>` +
      `<controlURL>/c</controlURL></service></device></root>`;
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: xxe },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      UnsafeXmlError,
    );
  });

  it("rejects a LOCATION whose host differs from the SSDP source", async () => {
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: VALID_DESCRIPTOR },
    });
    await assert.rejects(
      () =>
        fetchDeviceDescription(
          "http://192.168.1.20:5000/rootDesc.xml",
          SOURCE,
          { fetcher },
        ),
      /does not match SSDP source/,
    );
    assert.equal(fetcher.requests.length, 0);
  });

  it("rejects a serviceType containing header-injection characters", async () => {
    const malicious = VALID_DESCRIPTOR.replace(
      "urn:schemas-upnp-org:service:AVTransport:1",
      "urn:schemas-upnp-org:service:AVTransport:1\r\nX-Evil: 1",
    );
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: malicious },
    });
    await assert.rejects(
      () => fetchDeviceDescription(LOCATION, SOURCE, { fetcher }),
      /not a valid UPnP URN/,
    );
  });

  it("enforces the configured size limit with a smaller cap", async () => {
    const fetcher = new MockHttpFetcher({
      "/rootDesc.xml": { body: VALID_DESCRIPTOR },
    });
    await assert.rejects(
      () =>
        fetchDeviceDescription(LOCATION, SOURCE, {
          fetcher,
          maxXmlBytes: 128,
        }),
      /exceeds limit/,
    );
  });
});
