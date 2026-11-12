import {
  HostAdmission,
  createDefaultMatrix,
  parseOrigin,
} from "../index.ts";

const host = new HostAdmission(createDefaultMatrix());
const documentOrigin = parseOrigin("https://app.example.com")!;

const crossOriginNoCorp = host.admit({
  documentOrigin,
  resourceUrl: "https://cdn.third-party.org/img.png",
  resourceMode: "no-cors",
  credentials: "omit",
  embedding: "require-corp",
  declaration: { corp: "none", corsAccess: false },
});
console.log("无 CORP 跨源嵌入:", crossOriginNoCorp);

const crossOriginWithCorp = host.admit({
  documentOrigin,
  resourceUrl: "https://cdn.third-party.org/img.png",
  resourceMode: "no-cors",
  credentials: "omit",
  embedding: "require-corp",
  declaration: { corp: "cross-origin", corsAccess: false },
});
console.log("带 cross-origin CORP:", crossOriginWithCorp);

const redirect = host.admitRedirectChain({
  documentOrigin,
  resourceMode: "no-cors",
  credentials: "omit",
  embedding: "require-corp",
  hops: [
    {
      resourceUrl: "https://cdn.example.com/redirect",
      declaration: { corp: "same-site", corsAccess: false },
    },
    {
      resourceUrl: "https://cdn.third-party.org/final",
      declaration: { corp: "cross-origin", corsAccess: false },
    },
  ],
});
console.log("重定向链结果:", redirect.allowed, redirect.evaluated.evaluations);
