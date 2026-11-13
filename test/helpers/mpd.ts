export interface MpdRepresentation {
  id: string;
  kid: string;
  licenseUrl: string;
  scheme?: string;
  psshBase64?: string;
  /** 在 Representation 内联 ContentProtection，而不是放在 AdaptationSet。 */
  inline?: boolean;
}

export const CENC_SCHEME_URI = 'urn:uuid:1077efec-c022-4bdf-9047-cc25718c72fe';

function contentProtectionXml(rep: MpdRepresentation): string {
  const schemeAttr = rep.scheme
    ? `    <ContentProtection schemeIdUri="${CENC_SCHEME_URI}" value="${rep.scheme}" dashif:laurl="${rep.licenseUrl}"/>
`
    : '';
  const psshXml = rep.psshBase64
    ? `      <cenc:pssh>${rep.psshBase64}</cenc:pssh>
`
    : '';
  return `${schemeAttr}    <ContentProtection
      schemeIdUri="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"
      cenc:default_KID="urn:uuid:${rep.kid}"
      dashif:laurl="${rep.licenseUrl}">
${psshXml}    </ContentProtection>`;
}

export function buildMpd(representations: MpdRepresentation[], adaptationSetId = '0'): string {
  const inline = representations.filter((rep) => rep.inline);
  const shared = representations.find((rep) => !rep.inline);
  const asLevelCp = shared
    ? `${shared.scheme ? `  <ContentProtection schemeIdUri="${CENC_SCHEME_URI}" value="${shared.scheme}" dashif:laurl="${shared.licenseUrl}"/>
` : ''}  <ContentProtection
      schemeIdUri="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"
      cenc:default_KID="urn:uuid:${shared.kid}"
      dashif:laurl="${shared.licenseUrl}"/>
`
    : '';
  const repXml = representations
    .map((rep) => {
      if (rep.inline) {
        return `    <Representation id="${rep.id}" bandwidth="100000">
${contentProtectionXml(rep)}
    </Representation>`;
      }
      return `    <Representation id="${rep.id}" bandwidth="100000"/>`;
    })
    .join('\n');

  return `<?xml version="1.0" encoding="UTF-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"
     xmlns:cenc="urn:mpeg:cenc:2013"
     xmlns:dashif="https://dashif.org/CPS"
     type="static" duration="PT30S">
  <Period id="0">
    <AdaptationSet id="${adaptationSetId}" contentType="video">
${asLevelCp}${repXml}
    </AdaptationSet>
  </Period>
</MPD>`;
}
