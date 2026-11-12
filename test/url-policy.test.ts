import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  formatHttpUrl,
  parseHttpUrl,
  resolveControlUrl,
  validateLocation,
  validateRedirect,
  validateUrlBase,
} from "../src/url-policy.ts";
import {
  InvalidLocationError,
  NonPrivateHostError,
  RedirectHostMismatchError,
  ControlUrlHostMismatchError,
  UnsafeSchemeError,
  UnsafePortError,
  UrlBaseHostMismatchError,
} from "../src/errors.ts";

const ANCHOR = "192.168.1.10";

describe("parseHttpUrl", () => {
  it("parses http URLs with default and explicit ports", () => {
    assert.deepEqual(parseHttpUrl("http://192.168.1.10/desc.xml"), {
      scheme: "http",
      host: "192.168.1.10",
      port: 80,
      path: "/desc.xml",
    });
    assert.equal(
      parseHttpUrl("http://10.0.0.5:49152/a/b?x=1#frag").port,
      49152,
    );
    assert.equal(
      parseHttpUrl("http://10.0.0.5:49152/a/b?x=1#frag").path,
      "/a/b?x=1",
    );
  });

  it("unwraps bracketed IPv6 literals", () => {
    const parsed = parseHttpUrl("http://[fd00::1]:8080/d");
    assert.equal(parsed.host, "fd00::1");
    assert.equal(parsed.port, 8080);
  });

  it("rejects https, non-IP hosts, credentials and bad ports", () => {
    assert.throws(
      () => parseHttpUrl("https://192.168.1.10/"),
      UnsafeSchemeError,
    );
    assert.throws(
      () => parseHttpUrl("http://router.example.com/"),
      InvalidLocationError,
    );
    assert.throws(
      () => parseHttpUrl("http://user:pass@192.168.1.10/"),
      InvalidLocationError,
    );
    assert.throws(
      () => parseHttpUrl("http://192.168.1.10:99999/"),
      /99999|Malformed URL/,
    );
    assert.throws(
      () => parseHttpUrl("http://192.168.1.10:0/"),
      UnsafePortError,
    );
    assert.throws(() => parseHttpUrl("http://192.168.1.10:8080x/"));
    assert.throws(() => parseHttpUrl("not a url"), InvalidLocationError);
  });

  it("rejects non-canonical IP encodings that URL would normalize", () => {
    // WHATWG URL would silently map these to other addresses (often
    // 127.0.0.1); the policy must reject them before that happens.
    const exotic = [
      "http://2130706433/",
      "http://0x7f.0.0.1/",
      "http://0177.0.0.1/",
      "http://0xc0a8010a/",
      "http://192.168.1.10./",
      "http://[v1.fe80::1]/",
    ];
    for (const url of exotic) {
      assert.throws(() => parseHttpUrl(url), InvalidLocationError, url);
    }
  });

  it("rejects userinfo before the WHATWG parser can strip it", () => {
    assert.throws(
      () => parseHttpUrl("http://attacker@192.168.1.10/"),
      InvalidLocationError,
    );
  });

  it("rejects backslash-based parser confusion URLs", () => {
    assert.throws(
      () => parseHttpUrl("http:\\\\evil.com\\x"),
      InvalidLocationError,
    );
    assert.throws(
      () => parseHttpUrl("http://192.168.1.10\\@evil.com/"),
      InvalidLocationError,
    );
  });
});

describe("validateLocation", () => {
  it("accepts a private host matching the SSDP source", () => {
    const parsed = validateLocation(
      "http://192.168.1.10:5000/rootDesc.xml",
      ANCHOR,
    );
    assert.equal(parsed.host, ANCHOR);
    assert.equal(parsed.port, 5000);
  });

  it("rejects a LOCATION pointing at another host (SSDP spoof pivot)", () => {
    assert.throws(
      () =>
        validateLocation("http://192.168.1.1/admin", "192.168.1.10"),
      InvalidLocationError,
    );
  });

  it("rejects public and loopback source addresses", () => {
    assert.throws(
      () => validateLocation("http://8.8.8.8/", "8.8.8.8"),
      NonPrivateHostError,
    );
    assert.throws(
      () => validateLocation("http://127.0.0.1/", "127.0.0.1"),
      NonPrivateHostError,
    );
  });

  it("rejects non-http schemes even when the host matches", () => {
    assert.throws(
      () => validateLocation("gopher://192.168.1.10/", ANCHOR),
      UnsafeSchemeError,
    );
  });
});

describe("validateRedirect", () => {
  it("allows same-host redirects", () => {
    const parsed = validateRedirect(
      "http://192.168.1.10:8080/new/desc.xml",
      ANCHOR,
    );
    assert.equal(parsed.host, ANCHOR);
  });

  it("rejects cross-host redirects including other internal machines", () => {
    assert.throws(
      () => validateRedirect("http://192.168.1.1/admin", ANCHOR),
      RedirectHostMismatchError,
    );
    assert.throws(
      () => validateRedirect("http://10.0.0.5/x", ANCHOR),
      RedirectHostMismatchError,
    );
    assert.throws(
      () => validateRedirect("http://8.8.8.8/x", ANCHOR),
      RedirectHostMismatchError,
    );
  });
});

describe("validateUrlBase", () => {
  it("accepts same-host URLBase and rejects foreign ones", () => {
    assert.equal(
      validateUrlBase("http://192.168.1.10:5000/base/", ANCHOR),
      "http://192.168.1.10:5000/base/",
    );
    assert.throws(
      () => validateUrlBase("http://10.0.0.99/base/", ANCHOR),
      UrlBaseHostMismatchError,
    );
    assert.equal(validateUrlBase(undefined, ANCHOR), undefined);
  });
});

describe("resolveControlUrl", () => {
  const base = "http://192.168.1.10:5000/desc/device.xml";

  it("resolves relative controlURLs against the base", () => {
    const resolved = resolveControlUrl("../ctl/AVTransport", base, ANCHOR);
    assert.equal(
      formatHttpUrl(resolved),
      "http://192.168.1.10:5000/ctl/AVTransport",
    );
  });

  it("rejects absolute controlURLs targeting another internal host", () => {
    assert.throws(
      () =>
        resolveControlUrl(
          "http://192.168.1.20:49152/ctl/AVTransport",
          base,
          ANCHOR,
        ),
      ControlUrlHostMismatchError,
    );
    assert.throws(
      () => resolveControlUrl("http://10.1.2.3/ctl", base, ANCHOR),
      ControlUrlHostMismatchError,
    );
  });

  it("rejects protocol-relative URLs whose host differs", () => {
    assert.throws(
      () => resolveControlUrl("//192.168.1.20/ctl", base, ANCHOR),
      ControlUrlHostMismatchError,
    );
  });
});
