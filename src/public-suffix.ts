/**
 * Small, dependency-free public suffix matcher.
 *
 * The bundled rule set covers the multi-label ICANN suffixes and private
 * suffixes most likely to be encountered by an HTTP stack. A complete PSL
 * snapshot can be supplied with {@link createPublicSuffixMatcher}; this keeps
 * the package self-contained while preserving the PSL matching algorithm
 * (including wildcard (`*`) and exception (`!`) rules).
 */

const BUNDLED_RULES = `
# Common multi-label ICANN suffixes
ac.uk
co.uk
gov.uk
ltd.uk
me.uk
net.uk
nhs.uk
org.uk
plc.uk
police.uk
sch.uk
com.au
edu.au
gov.au
id.au
net.au
org.au
asn.au
co.jp
ac.jp
ad.jp
ed.jp
go.jp
gr.jp
lg.jp
ne.jp
or.jp
com.br
art.br
blog.br
edu.br
gov.br
mil.br
net.br
org.br
pro.br
wiki.br
co.in
ac.in
edu.in
firm.in
gen.in
gov.in
ind.in
mil.in
net.in
nic.in
org.in
res.in
com.cn
ac.cn
edu.cn
gov.cn
mil.cn
net.cn
org.cn
com.tw
club.tw
ebiz.tw
edu.tw
game.tw
gov.tw
idv.tw
net.tw
org.tw
co.kr
ac.kr
es.kr
go.kr
hs.kr
kg.kr
ms.kr
ne.kr
or.kr
pe.kr
re.kr
sc.kr
co.nz
ac.nz
geek.nz
gen.nz
govt.nz
kiwi.nz
maori.nz
net.nz
org.nz
school.nz
com.sg
edu.sg
gov.sg
net.sg
org.sg
per.sg
com.hk
edu.hk
gov.hk
idv.hk
net.hk
org.hk
com.mo
edu.mo
gov.mo
net.mo
org.mo
com.mx
edu.mx
gob.mx
net.mx
org.mx
com.ar
edu.ar
gob.ar
net.ar
org.ar
tur.ar
com.tr
av.tr
bel.tr
biz.tr
dr.tr
edu.tr
gen.tr
gov.tr
info.tr
k12.tr
kep.tr
mil.tr
name.tr
net.tr
org.tr
pol.tr
tel.tr
tsk.tr
tv.tr
web.tr
co.za
ac.za
edu.za
gov.za
net.za
org.za
web.za
co.il
ac.il
edu.il
gov.il
idf.il
k12.il
muni.il
net.il
org.il
co.id
ac.id
go.id
mil.id
net.id
or.id
org.id
ponpes.id
sch.id
web.id
com.my
edu.my
gov.my
mil.my
name.my
net.my
org.my
com.ph
edu.ph
gov.ph
mil.ph
net.ph
ngo.ph
org.ph
com.vn
ac.vn
biz.vn
edu.vn
gov.vn
health.vn
info.vn
name.vn
net.vn
org.vn
pro.vn
com.pk
edu.pk
gob.pk
gov.pk
net.pk
org.pk
web.pk
com.bd
ac.bd
edu.bd
gov.bd
mil.bd
net.bd
org.bd
com.np
edu.np
gov.np
mil.np
net.np
org.np
com.lk
edu.lk
gov.lk
mil.lk
net.lk
ngo.lk
org.lk
soc.lk
web.lk
com.ua
edu.ua
gov.ua
in.ua
net.ua
org.ua
com.ru
msk.ru
net.ru
org.ru
pp.ru
spb.ru
co.ke
ac.ke
edu.ke
go.ke
info.ke
me.ke
mobi.ke
ne.ke
net.ke
org.ke
sc.ke
com.eg
edu.eg
gov.eg
mil.eg
net.eg
org.eg
sci.eg
com.sa
edu.sa
gov.sa
med.sa
net.sa
org.sa
pub.sa
sch.sa
com.ng
edu.ng
gov.ng
i.ng
mil.ng
net.ng
org.ng
com.gh
edu.gh
gov.gh
net.gh
org.gh
com.ug
co.ug
edu.ug
gov.ug
mil.ug
ne.ug
org.ug
co.tz
com.tz
edu.tz
gov.tz
mil.tz
org.tz
co.zw
com.zw
edu.zw
gov.zw
org.zw
co.bw
org.bw
co.ao
com.ao
edu.ao
gov.ao
net.ao
org.ao
co.mz
com.mz
edu.mz
gov.mz
org.mz
co.ve
com.ve
edu.ve
gov.ve
mil.ve
net.ve
org.ve
com.co
edu.co
gov.co
mil.co
net.co
org.co
com.pe
edu.pe
gob.pe
mil.pe
net.pe
org.pe
com.ec
edu.ec
gov.ec
mil.ec
net.ec
org.ec
com.uy
edu.uy
gub.uy
mil.uy
net.uy
org.uy
com.bo
edu.bo
gov.bo
mil.bo
net.bo
org.bo
tv.bo
com.py
edu.py
gov.py
mil.py
net.py
org.py
com.do
edu.do
gov.do
mil.do
net.do
org.do
com.gt
edu.gt
gob.gt
ind.gt
mil.gt
net.gt
org.gt
com.sv
edu.sv
gob.sv
mil.sv
net.sv
org.sv
com.hn
edu.hn
gob.hn
mil.hn
net.hn
org.hn
com.ni
edu.ni
gob.ni
net.ni
org.ni
com.pa
edu.pa
gob.pa
mil.pa
net.pa
org.pa
com.cr
ac.cr
edu.cr
fi.cr
go.cr
mil.cr
net.cr
org.cr
com.jm
edu.jm
gov.jm
mil.jm
net.jm
org.jm
com.ht
edu.ht
gov.ht
net.ht
org.ht
com.cu
edu.cu
gov.cu
inf.cu
net.cu
org.cu
com.et
edu.et
gov.et
net.et
org.et
com.fj
edu.fj
gov.fj
net.fj
org.fj
com.mt
edu.mt
gov.mt
net.mt
org.mt
com.mu
edu.mu
gov.mu
net.mu
org.mu
com.cy
ac.cy
gov.cy
net.cy
org.cy
com.mk
edu.mk
gov.mk
net.mk
org.mk
com.al
edu.al
gov.al
net.al
org.al
com.ba
edu.ba
gov.ba
mil.ba
net.ba
org.ba
com.me
ac.me
edu.me
gov.me
net.me
org.me

# Japan wildcard/exception examples
*.kawasaki.jp
!city.kawasaki.jp
*.kitakyushu.jp
!city.kitakyushu.jp
*.kobe.jp
!city.kobe.jp
*.nagoya.jp
!city.nagoya.jp
*.sapporo.jp
!city.sapporo.jp
*.sendai.jp
!city.sendai.jp
*.yokohama.jp
!city.yokohama.jp
*.ck
!www.ck

# Private suffixes: each tenant gets its own registrable domain
github.io
githubusercontent.com
gitlab.io
bitbucket.io
blogspot.com
wordpress.com
wpcomstaging.com
tumblr.com
substack.com
medium.com
ghost.io
herokuapp.com
appspot.com
firebaseapp.com
web.app
pages.dev
workers.dev
vercel.app
netlify.app
fly.dev
railway.app
onrender.com
supabase.co
azurewebsites.net
cloudapp.net
cloudfront.net
amazonaws.com
googleusercontent.com
googleapis.com
myshopify.com
shopifypreview.com
wixsite.com
weebly.com
squarespace.com
webflow.io
webnode.page
jimdo.com
jimdosite.com
yolasite.com
business.site
notion.site
readthedocs.io
neocities.org
carrd.co
codeberg.page
deno.dev
val.town
glitch.me
replit.app
replit.dev
repl.co
ngrok.io
ngrok.app
ngrok-free.app
trycloudflare.com
`;

export interface PublicSuffixRules {
  readonly exact: ReadonlySet<string>;
  readonly wildcard: ReadonlySet<string>;
  readonly exception: ReadonlySet<string>;
}

export interface PublicSuffixMatcher {
  readonly rules: PublicSuffixRules;
  getPublicSuffix(hostname: string | URL): string | null;
  getRegistrableDomain(hostname: string | URL): string | null;
}

export function parsePublicSuffixRules(text: string): PublicSuffixRules {
  const exact = new Set<string>();
  const wildcard = new Set<string>();
  const exception = new Set<string>();

  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;

    if (line.startsWith("!")) {
      const rule = normalizeRuleHost(line.slice(1));
      if (rule) exception.add(rule);
      continue;
    }

    if (line.startsWith("*.")) {
      const rule = normalizeRuleHost(line.slice(2));
      if (rule) wildcard.add(rule);
      continue;
    }

    const rule = normalizeRuleHost(line);
    if (rule) exact.add(rule);
  }

  return Object.freeze({ exact, wildcard, exception });
}

function normalizeRuleHost(value: string): string | null {
  const host = value.trim().toLowerCase().replace(/\.+$/u, "");
  if (!host || host.includes("/") || host.includes("\\")) return null;
  return host;
}

/**
 * Normalize a hostname or an absolute URL to the host used for PSL matching.
 * URL parsing already performs IDNA and IPv4 canonicalization for us.
 */
export function normalizeHostname(value: string | URL): string | null {
  let raw: string;
  if (value instanceof URL) {
    raw = value.hostname;
  } else {
    const input = value.trim();
    if (!input) return null;

    if (/^[a-z][a-z0-9+.-]*:\/\//iu.test(input)) {
      try {
        raw = new URL(input).hostname;
      } catch {
        return null;
      }
    } else {
      raw = input;
      const at = raw.lastIndexOf("@");
      if (at >= 0) raw = raw.slice(at + 1);

      if (raw.startsWith("[")) {
        const closing = raw.indexOf("]");
        if (closing >= 0) raw = raw.slice(0, closing + 1);
      } else {
        const slash = raw.search(/[\/?#]/u);
        if (slash >= 0) raw = raw.slice(0, slash);
      }
    }
  }

  const host = raw.toLowerCase().replace(/\.+$/u, "");
  return host || null;
}

export function isIpAddress(hostname: string): boolean {
  if (hostname.startsWith("[") && hostname.endsWith("]")) return true;

  const octets = hostname.split(".");
  if (octets.length !== 4) return false;
  return octets.every((octet) => {
    if (!/^[0-9]+$/u.test(octet)) return false;
    const number = Number(octet);
    return number >= 0 && number <= 255;
  });
}

function labelsFor(hostname: string): string[] {
  return hostname.split(".").filter(Boolean);
}

function matchPublicSuffix(hostname: string, rules: PublicSuffixRules): string | null {
  if (isIpAddress(hostname)) return null;

  const labels = labelsFor(hostname);
  if (labels.length === 0) return null;

  // Exception rules win over wildcard rules. For `!www.ck`, `www.ck` is a
  // registrable domain and its public suffix is `ck`.
  for (let index = 0; index < labels.length; index += 1) {
    const suffix = labels.slice(index).join(".");
    if (rules.exception.has(suffix)) {
      return labels.slice(index + 1).join(".") || null;
    }
  }

  // The implicit default rule is `*`: the rightmost label is a public suffix.
  let suffixStart = labels.length - 1;

  for (let index = 0; index < labels.length; index += 1) {
    const suffix = labels.slice(index).join(".");

    if (rules.exact.has(suffix)) {
      suffixStart = Math.min(suffixStart, index);
    }

    const parent = labels.slice(index + 1).join(".");
    if (rules.wildcard.has(parent)) {
      suffixStart = Math.min(suffixStart, index);
    }
  }

  return labels.slice(suffixStart).join(".");
}

export function createPublicSuffixMatcher(rulesText: string = BUNDLED_RULES): PublicSuffixMatcher {
  const rules = parsePublicSuffixRules(rulesText);

  const getPublicSuffix = (hostname: string | URL): string | null => {
    const normalized = normalizeHostname(hostname);
    if (!normalized) return null;
    return matchPublicSuffix(normalized, rules);
  };

  const getRegistrableDomain = (hostname: string | URL): string | null => {
    const normalized = normalizeHostname(hostname);
    if (!normalized || isIpAddress(normalized)) return null;

    const publicSuffix = matchPublicSuffix(normalized, rules);
    if (!publicSuffix) return null;

    const labels = labelsFor(normalized);
    const suffixLabels = labelsFor(publicSuffix);
    if (labels.length <= suffixLabels.length) return null;

    return labels.slice(-(suffixLabels.length + 1)).join(".");
  };

  return Object.freeze({ rules, getPublicSuffix, getRegistrableDomain });
}

const defaultMatcher = createPublicSuffixMatcher();

export function getPublicSuffix(hostname: string | URL): string | null {
  return defaultMatcher.getPublicSuffix(typeof hostname === "string" ? hostname : hostname.hostname);
}

export function getRegistrableDomain(hostname: string | URL): string | null {
  return defaultMatcher.getRegistrableDomain(
    typeof hostname === "string" ? hostname : hostname.hostname,
  );
}

export const registrableDomain = getRegistrableDomain;
export const publicSuffix = getPublicSuffix;
