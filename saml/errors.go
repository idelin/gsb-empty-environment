package saml

import "errors"

// Package-level sentinel errors. Callers can use errors.Is to distinguish a
// rejected document from a genuine processing failure.
var (
	// ErrMalformed is returned for any document that cannot be decoded with a
	// real XML parser, or that carries forbidden constructs such as a DTD.
	ErrMalformed = errors.New("saml: malformed XML document")

	// ErrDuplicateID is returned when the document-wide index encounters the
	// same ID more than once. This rejects same-ID signature wrapping, where
	// an attacker adds a second element reusing the signed element's ID.
	ErrDuplicateID = errors.New("saml: duplicate element ID in document")

	// ErrNamespaceSubstitution is returned when an element whose local name
	// belongs to the SAML or XML-DSig vocabulary is bound to a different
	// namespace URI, including xmlns rebinding of an existing prefix.
	ErrNamespaceSubstitution = errors.New("saml: namespace substitution detected")

	// ErrNoSignature is returned when the document contains no verifiable
	// ds:Signature element.
	ErrNoSignature = errors.New("saml: no signature found")

	// ErrReferenceTarget is returned when a signature reference does not point
	// at a single element that exists in the document index.
	ErrReferenceTarget = errors.New("saml: signature reference target not found")

	// ErrNotAssertion is returned when a verified signature points at an
	// element that is not a SAML assertion.
	ErrNotAssertion = errors.New("saml: signed element is not an assertion")

	// ErrAmbiguousAssertion is returned when more than one assertion is
	// independently verified, which would make the consumed subject
	// ambiguous.
	ErrAmbiguousAssertion = errors.New("saml: multiple verified assertions")

	// ErrReplay is returned by ReplayStore.Consume when the ID has already
	// been consumed.
	ErrReplay = errors.New("saml: assertion ID has already been consumed")

	// ErrCondition is returned for missing or invalid time conditions.
	ErrCondition = errors.New("saml: invalid assertion time conditions")
)
