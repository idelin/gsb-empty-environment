import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  INSUFFICIENT_STORAGE,
  applyNResults,
  buildVisibleVCard,
  cardMatchesFilter,
  getEtag,
  isHiddenProperty,
  runAddressBookQuery,
  type AddressBookContact,
  type AddressBookFilter,
  type AddressBookQuery,
  type PropFilter,
} from "../index.ts";

const readonlyPolicy = { canSeeHidden: false } as const;
const ownerPolicy = { canSeeHidden: true } as const;

const alice = [
  "BEGIN:VCARD",
  "VERSION:3.0",
  "FN:Alice Anderson",
  "N:Anderson;Alice;;;",
  "NOTE:top secret acquisition plans",
  "X-PRIVATE-REF:deal-42",
  "END:VCARD",
].join("\r\n");

const bob = [
  "BEGIN:VCARD",
  "VERSION:3.0",
  "FN:Bob Brown",
  "N:Brown;Bob;;;",
  "NOTE:another secret note",
  "x-private-ref:bob-private",
  "END:VCARD",
].join("\r\n");

const carol = [
  "BEGIN:VCARD",
  "VERSION:3.0",
  "FN:Carol Clark",
  "N:Clark;Carol;;;",
  "END:VCARD",
].join("\r\n");

function noteTextMatch(
  substring: string,
  negateCondition = false,
): AddressBookFilter {
  return {
    test: "anyof",
    propFilters: [
      {
        name: "NOTE",
        isNotDefined: false,
        textMatch: {
          collation: "i;ascii-casemap",
          negateCondition,
          substring,
        },
      },
    ],
  };
}

function noteNotDefinedFilter(): AddressBookFilter {
  const propFilter: PropFilter = {
    name: "NOTE",
    isNotDefined: true,
    textMatch: null,
  };
  return { test: "anyof", propFilters: [propFilter] };
}

function fnTextMatch(substring: string): AddressBookFilter {
  return {
    test: "anyof",
    propFilters: [
      {
        name: "FN",
        isNotDefined: false,
        textMatch: {
          collation: "i;ascii-casemap",
          negateCondition: false,
          substring,
        },
      },
    ],
  };
}

const contacts: AddressBookContact[] = [
  { href: "/contacts/alice.vcf", vcard: alice },
  { href: "/contacts/bob.vcf", vcard: bob },
  { href: "/contacts/carol.vcf", vcard: carol },
];

describe("hidden property classification", () => {
  it("hides NOTE and X-PRIVATE-* case-insensitively", () => {
    assert.equal(isHiddenProperty("NOTE"), true);
    assert.equal(isHiddenProperty("note"), true);
    assert.equal(isHiddenProperty("X-PRIVATE-REF"), true);
    assert.equal(isHiddenProperty("x-private-ref"), true);
    assert.equal(isHiddenProperty("X-PRIVATE"), false);
    assert.equal(isHiddenProperty("FN"), false);
    assert.equal(isHiddenProperty("X-PUBLIC-REF"), false);
  });
});

describe("text-match guessing of hidden NOTE", () => {
  it("treats hidden NOTE as non-existent: no text-match hits for read-only members", () => {
    const filter = noteTextMatch("acquisition");
    assert.equal(cardMatchesFilter(alice, filter, readonlyPolicy), false);
    assert.equal(cardMatchesFilter(bob, filter, readonlyPolicy), false);
    assert.equal(cardMatchesFilter(carol, filter, readonlyPolicy), false);
  });

  it("still matches hidden NOTE for owners", () => {
    const filter = noteTextMatch("acquisition");
    assert.equal(cardMatchesFilter(alice, filter, ownerPolicy), true);
    assert.equal(cardMatchesFilter(bob, filter, ownerPolicy), false);
  });

  it("does not allow negated text-match to probe hidden values", () => {
    const filter = noteTextMatch("acquisition", true);
    assert.equal(cardMatchesFilter(alice, filter, readonlyPolicy), false);
    assert.equal(cardMatchesFilter(bob, filter, readonlyPolicy), false);
    assert.equal(cardMatchesFilter(carol, filter, readonlyPolicy), false);
  });

  it("supports case-insensitive collation on visible properties", () => {
    assert.equal(
      cardMatchesFilter(alice, fnTextMatch("ALICE"), readonlyPolicy),
      true,
    );
  });

  it("makes is-not-defined true for hidden NOTE of read-only members", () => {
    const filter = noteNotDefinedFilter();
    assert.equal(cardMatchesFilter(alice, filter, readonlyPolicy), true);
    assert.equal(cardMatchesFilter(bob, filter, readonlyPolicy), true);
    assert.equal(cardMatchesFilter(carol, filter, readonlyPolicy), true);

    assert.equal(cardMatchesFilter(alice, filter, ownerPolicy), false);
    assert.equal(cardMatchesFilter(carol, filter, ownerPolicy), true);
  });

  it("treats hidden X-PRIVATE-* filters as absent for read-only members", () => {
    const filter: AddressBookFilter = {
      test: "anyof",
      propFilters: [
        {
          name: "X-PRIVATE-REF",
          isNotDefined: true,
          textMatch: null,
        },
      ],
    };
    assert.equal(cardMatchesFilter(alice, filter, readonlyPolicy), true);
    assert.equal(cardMatchesFilter(alice, filter, ownerPolicy), false);
  });

  it("keeps visible filters working under allof", () => {
    const filter: AddressBookFilter = {
      test: "allof",
      propFilters: [
        {
          name: "FN",
          isNotDefined: false,
          textMatch: {
            collation: "i;ascii-casemap",
            negateCondition: false,
            substring: "Alice",
          },
        },
        {
          name: "NOTE",
          isNotDefined: false,
          textMatch: {
            collation: "i;ascii-casemap",
            negateCondition: false,
            substring: "acquisition",
          },
        },
      ],
    };
    assert.equal(cardMatchesFilter(alice, filter, readonlyPolicy), false);
    assert.equal(cardMatchesFilter(alice, filter, ownerPolicy), true);
  });
});

describe("nresults limit and truncation", () => {
  const query: AddressBookQuery = {
    filter: fnTextMatch(""),
    nresults: 2,
    addressData: null,
  };

  it("counts only visible matches and reports truncation with 507", () => {
    const result = runAddressBookQuery(contacts, query, readonlyPolicy);
    assert.equal(result.totalVisibleMatches, 3);
    assert.equal(result.contacts.length, 2);
    assert.equal(result.truncated, true);
    assert.equal(result.limitStatus, INSUFFICIENT_STORAGE);
    assert.deepEqual(
      result.contacts.map((contact) => contact.href),
      ["/contacts/alice.vcf", "/contacts/bob.vcf"],
    );
  });

  it("does not truncate when nresults covers every visible match", () => {
    const result = runAddressBookQuery(
      contacts,
      { ...query, nresults: 3 },
      readonlyPolicy,
    );
    assert.equal(result.contacts.length, 3);
    assert.equal(result.truncated, false);
    assert.equal(result.limitStatus, null);
  });

  it("excludes hidden-property matches from nresults counting", () => {
    const guessingQuery: AddressBookQuery = {
      filter: noteTextMatch("secret"),
      nresults: 1,
      addressData: null,
    };
    const readonlyResult = runAddressBookQuery(
      contacts,
      guessingQuery,
      readonlyPolicy,
    );
    assert.equal(readonlyResult.totalVisibleMatches, 0);
    assert.equal(readonlyResult.contacts.length, 0);
    assert.equal(readonlyResult.truncated, false);

    const ownerResult = runAddressBookQuery(
      contacts,
      guessingQuery,
      ownerPolicy,
    );
    assert.equal(ownerResult.totalVisibleMatches, 2);
    assert.equal(ownerResult.truncated, true);
  });

  it("keeps counts stable when hidden content changes", () => {
    const before = runAddressBookQuery(contacts, query, readonlyPolicy);
    const modified = contacts.map((contact) =>
      contact.href.endsWith("alice.vcf")
        ? {
            ...contact,
            vcard: alice.replace("top secret acquisition plans", "totally different"),
          }
        : contact,
    );
    const after = runAddressBookQuery(modified, query, readonlyPolicy);
    assert.equal(after.totalVisibleMatches, before.totalVisibleMatches);
    assert.deepEqual(
      after.contacts.map((contact) => contact.href),
      before.contacts.map((contact) => contact.href),
    );
  });

  it("exposes the same truncation semantics via applyNResults helper", () => {
    const page = applyNResults([1, 2, 3, 4], 2);
    assert.deepEqual(page.items, [1, 2]);
    assert.equal(page.totalMatches, 4);
    assert.equal(page.truncated, true);
    assert.equal(page.limitStatus, INSUFFICIENT_STORAGE);
  });
});

describe("getetag visibility", () => {
  it("does not change read-only etag when NOTE or X-PRIVATE-* changes", () => {
    const aliceWithDifferentNote = alice
      .replace("top secret acquisition plans", "rewritten hidden note")
      .replace("X-PRIVATE-REF:deal-42", "X-PRIVATE-REF:deal-99");

    assert.equal(
      getEtag(alice, readonlyPolicy),
      getEtag(aliceWithDifferentNote, readonlyPolicy),
    );
  });

  it("produces different etags for owner when hidden content changes", () => {
    const aliceWithDifferentNote = alice.replace(
      "top secret acquisition plans",
      "rewritten hidden note",
    );
    assert.notEqual(
      getEtag(alice, ownerPolicy),
      getEtag(aliceWithDifferentNote, ownerPolicy),
    );
  });

  it("changes read-only etag when a visible property changes", () => {
    const renamed = alice.replace("Alice Anderson", "Alice Zephyr");
    assert.notEqual(
      getEtag(alice, readonlyPolicy),
      getEtag(renamed, readonlyPolicy),
    );
  });

  it("is deterministic for the same caller view", () => {
    assert.equal(getEtag(alice, readonlyPolicy), getEtag(alice, readonlyPolicy));
  });

  it("strips hidden lines from the read-only query payload", () => {
    const visible = buildVisibleVCard(alice, readonlyPolicy, null);
    assert.ok(!visible.includes("NOTE"));
    assert.ok(!visible.includes("X-PRIVATE"));
    assert.ok(visible.includes("FN:Alice Anderson"));
  });
});

describe("address-data property requests", () => {
  it("treats requests for hidden properties as not requested", () => {
    const payload = buildVisibleVCard(
      alice,
      readonlyPolicy,
      { allProps: false, properties: ["FN", "NOTE", "X-PRIVATE-REF"] },
    );
    assert.ok(payload.includes("FN:Alice Anderson"));
    assert.ok(!payload.includes("NOTE"));
    assert.ok(!payload.includes("X-PRIVATE"));
  });

  it("returns a structural vcard when only hidden properties are requested", () => {
    const payload = buildVisibleVCard(
      alice,
      readonlyPolicy,
      { allProps: false, properties: ["NOTE"] },
    );
    assert.ok(payload.startsWith("BEGIN:VCARD\r\n"));
    assert.ok(payload.endsWith("END:VCARD\r\n"));
    assert.ok(!payload.includes("NOTE"));
  });

  it("keeps requested hidden properties for owners", () => {
    const payload = buildVisibleVCard(
      alice,
      ownerPolicy,
      { allProps: false, properties: ["FN", "NOTE"] },
    );
    assert.ok(payload.includes("FN:Alice Anderson"));
    assert.ok(payload.includes("NOTE:top secret acquisition plans"));
  });

  it("strips hidden properties in all-props mode for read-only members", () => {
    const payload = buildVisibleVCard(
      alice,
      readonlyPolicy,
      { allProps: true, properties: [] },
    );
    assert.ok(!payload.includes("NOTE"));
    assert.ok(!payload.includes("PRIVATE"));
    assert.ok(payload.includes("N:Anderson;Alice;;;"));
  });
});

describe("end-to-end REPORT shape", () => {
  it("returns filtered, limited, sanitized contacts with per-caller etags", () => {
    const result = runAddressBookQuery(
      contacts,
      {
        filter: fnTextMatch("a"),
        nresults: 10,
        addressData: { allProps: false, properties: ["FN", "NOTE"] },
      },
      readonlyPolicy,
    );

    assert.equal(result.truncated, false);
    assert.ok(result.contacts.every((contact) => !contact.visibleVCard.includes("NOTE")));
    assert.ok(result.contacts.every((contact) => contact.etag.startsWith('"')));
    assert.deepEqual(
      result.contacts.map((contact) => contact.href),
      ["/contacts/alice.vcf", "/contacts/carol.vcf"],
    );
  });
});
