export {
  INSUFFICIENT_STORAGE,
  applyNResults,
  buildVisibleVCard,
  cardMatchesFilter,
  getEtag,
  isHiddenProperty,
  isPropertyVisible,
  runAddressBookQuery,
  type AddressBookContact,
  type AddressBookFilter,
  type AddressBookQuery,
  type AddressBookQueryResult,
  type AddressDataRequest,
  type CollationKind,
  type MatchedContact,
  type NResultsPage,
  type PropFilter,
  type TextMatch,
  type VisibilityPolicy,
} from "./src/visibility.ts";

export {
  parseVCard,
  serializeVCard,
  unescapeText,
  type VCard,
  type VCardProperty,
} from "./src/vcard.ts";
