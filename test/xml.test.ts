import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  childText,
  findAll,
  findChild,
  parseXml,
} from "../src/xml.ts";
import { MalformedXmlError, UnsafeXmlError } from "../src/errors.ts";

describe("parseXml", () => {
  it("parses elements, attributes, text and CDATA", () => {
    const root = parseXml(
      `<?xml version="1.0"?>
       <root xmlns="urn:schemas-upnp-org:device-1-0">
         <device>
           <friendlyName>TV &amp; Box</friendlyName>
           <spec><![CDATA[raw <xml> & data]]></spec>
           <service serviceId="id1" />
           <service serviceId="id2" />
         </device>
       </root>`,
    );
    assert.equal(root.tag, "root");
    const device = findChild(root, "device")!;
    assert.equal(childText(device, "friendlyName"), "TV & Box");
    assert.equal(childText(device, "spec"), "raw <xml> & data");
    assert.equal(findAll(device, "service").length, 2);
    assert.equal(findChild(device, "service")?.attributes.serviceId, "id1");
  });

  it("resolves the five predefined entities and numeric references", () => {
    const root = parseXml(
      `<root><a>&lt;&gt;&quot;&apos;&amp; &#65; &#x42;</a></root>`,
    );
    assert.equal(childText(root, "a"), `<>"'& A B`);
  });

  it("rejects DOCTYPE declarations (external entity / XXE vector)", () => {
    const xxe =
      `<?xml version="1.0"?>` +
      `<!DOCTYPE root [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` +
      `<root>&xxe;</root>`;
    assert.throws(() => parseXml(xxe), UnsafeXmlError);

    const externalDtd =
      `<!DOCTYPE root SYSTEM "http://192.168.1.20/evil.dtd"><root/>`;
    assert.throws(() => parseXml(externalDtd), UnsafeXmlError);
  });

  it("rejects custom entity references even without a DTD", () => {
    assert.throws(
      () => parseXml("<root>&billionLaughs;</root>"),
      UnsafeXmlError,
    );
  });

  it("rejects ENTITY declarations and parameter entities", () => {
    assert.throws(
      () => parseXml(`<!DOCTYPE root [<!ENTITY a "b">]><root/>`),
      UnsafeXmlError,
    );
    // A stray declaration inside the document is never parsed as content.
    assert.throws(
      () => parseXml(`<root><!ENTITY a "b"><![CDATA[x]]></root>`),
      MalformedXmlError,
    );
  });

  it("rejects malformed documents", () => {
    assert.throws(() => parseXml("<root><a></root>"), MalformedXmlError);
    assert.throws(() => parseXml(""), MalformedXmlError);
    assert.throws(() => parseXml("<root>text"), MalformedXmlError);
    assert.throws(() => parseXml("<!-- unterminated"), MalformedXmlError);
  });

  it("strips namespace prefixes from tag names", () => {
    const root = parseXml(
      `<s:Envelope xmlns:s="http://x"><s:Body>v</s:Body></s:Envelope>`,
    );
    assert.equal(root.tag, "Envelope");
    assert.equal(findChild(root, "Body")?.text, "v");
  });

  it("skips comments and processing instructions inside elements", () => {
    const root = parseXml(
      `<root><!-- header --><device><?app note?>
         <service /><!-- tail --></device></root>`,
    );
    const device = findChild(root, "device")!;
    assert.equal(findChild(device, "service")?.tag, "service");
  });

  it("rejects malformed entity and out-of-range numeric references", () => {
    assert.throws(() => parseXml("<a>&;</a>"), UnsafeXmlError);
    assert.throws(() => parseXml("<a>&unknown;</a>"), UnsafeXmlError);
    assert.throws(() => parseXml("<a>&#xFFFFFFFF;</a>"), MalformedXmlError);
    assert.throws(() => parseXml("<a>&#999999999;</a>"), MalformedXmlError);
  });
});
