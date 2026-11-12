package netsec

import (
	"errors"
	"strings"
	"testing"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"

	uuidA1  = "11111111-1111-1111-1111-111111111111"
	uuidA2  = "22222222-2222-2222-2222-222222222222"
	uuidA3  = "33333333-3333-3333-3333-333333333333"
	uuidA4  = "44444444-4444-4444-4444-444444444444"
	uuidB1  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	uuidEph = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	mustRegister(t, r, Secret{
		UUID: uuidA1, Usage: "ceph-rbd", TenantID: tenantA,
		Value: "a1-secret-value",
	})
	mustRegister(t, r, Secret{
		UUID: uuidA2, Usage: "luks-vault", TenantID: tenantA,
		Private: true, Value: "a2-PRIVATE-value",
	})
	// Same usage name in another tenant: usage resolution must stay tenant-scoped.
	mustRegister(t, r, Secret{
		UUID: uuidB1, Usage: "ceph-rbd", TenantID: tenantB,
		Value: "b1-secret-value",
	})
	mustRegister(t, r, Secret{
		UUID: uuidA3, Usage: "shared-name", TenantID: tenantA, Value: "a3-value",
	})
	mustRegister(t, r, Secret{
		UUID: uuidA4, Usage: "shared-name", TenantID: tenantA, Value: "a4-value",
	})
	mustRegister(t, r, Secret{
		UUID: uuidEph, Usage: "temp-key", TenantID: tenantA,
		Ephemeral: true, Value: "ephemeral-value",
	})
	return r
}

func mustRegister(t *testing.T, r *Registry, s Secret) {
	t.Helper()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register(%s): %v", s.UUID, err)
	}
}

func TestValidate_CompliantReferences(t *testing.T) {
	r := newTestRegistry(t)
	fragment := []byte(`
<domain>
  <uuid>55555555-5555-5555-5555-555555555555</uuid>
  <devices>
    <disk>
      <auth username="vmuser">
        <secret type="ceph" uuid="` + uuidA1 + `"/>
      </auth>
    </disk>
    <disk>
      <encryption format="luks">
        <secret type="luks" usage="luks-vault"/>
      </encryption>
    </disk>
  </devices>
</domain>`)

	got, err := r.ValidateDomainSecrets(fragment, tenantA)
	if err != nil {
		t.Fatalf("compliant XML should pass, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 resolved secrets, got %d", len(got))
	}
	if got[0].UUID != uuidA1 || got[1].Usage != "luks-vault" {
		t.Fatalf("unexpected resolution content/order: %+v", got)
	}
}

func TestValidate_RejectsForeignTenantUUID(t *testing.T) {
	r := newTestRegistry(t)
	fragment := []byte(`<domain><devices><disk>
	  <auth username="x"><secret type="ceph" uuid="` + uuidB1 + `"/></auth>
	</disk></devices></domain>`)

	_, err := r.ValidateDomainSecrets(fragment, tenantA)
	if !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("expected ErrCrossTenant, got %v", err)
	}
	if !strings.Contains(err.Error(), "disk[0]/auth/secret") {
		t.Fatalf("error should name the offending path, got %v", err)
	}

	ownerXML := []byte(`<domain><devices><disk><auth><secret uuid="` + uuidB1 + `"/></auth></disk></devices></domain>`)
	if _, err := r.ValidateDomainSecrets(ownerXML, tenantB); err != nil {
		t.Fatalf("owner should resolve its own secret, got %v", err)
	}
}

func TestValidate_UnknownUUIDIsNotFound(t *testing.T) {
	r := newTestRegistry(t)
	fragment := []byte(`<domain><devices><disk><auth><secret uuid="99999999-9999-9999-9999-999999999999"/></auth></disk></devices></domain>`)
	_, err := r.ValidateDomainSecrets(fragment, tenantA)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestValidate_UsageIsTenantScopedAndUnambiguous(t *testing.T) {
	r := newTestRegistry(t)

	// "ceph-rbd" exists in both tenants; A must resolve only its own entry.
	unique := []byte(`<domain><devices><disk><auth><secret type="ceph" usage="ceph-rbd"/></auth></disk></devices></domain>`)
	got, err := r.ValidateDomainSecrets(unique, tenantA)
	if err != nil {
		t.Fatalf("unique in-tenant usage should resolve, got %v", err)
	}
	if got[0].UUID != uuidA1 {
		t.Fatalf("cross-tenant usage leak, resolved %s", got[0].UUID)
	}

	ambiguous := []byte(`<domain><devices><disk><auth><secret usage="shared-name"/></auth></disk></devices></domain>`)
	if _, err := r.ValidateDomainSecrets(ambiguous, tenantA); !errors.Is(err, ErrUsageAmbiguous) {
		t.Fatalf("expected ErrUsageAmbiguous, got %v", err)
	}

	// Tenant B has no "shared-name"; it must see not-found, never A's entries.
	if _, err := r.ValidateDomainSecrets(ambiguous, tenantB); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("tenant B should see not-found, got %v", err)
	}
}

func TestPrivateSecretValueNeverReadable(t *testing.T) {
	r := newTestRegistry(t)

	// Referencing a private secret in domain XML is allowed and returns
	// metadata only, never the value.
	fragment := []byte(`<domain><devices><disk><encryption><secret uuid="` + uuidA2 + `"/></encryption></disk></devices></domain>`)
	infos, err := r.ValidateDomainSecrets(fragment, tenantA)
	if err != nil {
		t.Fatalf("private reference should validate, got %v", err)
	}
	if !infos[0].Private {
		t.Fatalf("expected Private flag in descriptor")
	}

	if _, err := r.GetValue(SecretRef{Kind: RefUUID, UUID: uuidA2}, tenantA); !errors.Is(err, ErrPrivateValueNotReadable) {
		t.Fatalf("owner must not read private value by uuid, got %v", err)
	}
	if _, err := r.GetValue(SecretRef{Kind: RefUsage, Usage: "luks-vault"}, tenantA); !errors.Is(err, ErrPrivateValueNotReadable) {
		t.Fatalf("private value lookup by usage must fail, got %v", err)
	}

	// A non-private secret of the same tenant remains readable.
	val, err := r.GetValue(SecretRef{Kind: RefUUID, UUID: uuidA1}, tenantA)
	if err != nil || val != "a1-secret-value" {
		t.Fatalf("readable secret mismatch: val=%q err=%v", val, err)
	}

	// Every query surface returns value-free descriptors: SecretInfo has no
	// field capable of carrying secret material, so just confirm the private
	// entry is listed with its flag intact rather than exposing its value.
	var listedPrivate bool
	for _, info := range r.ListForTenant(tenantA) {
		if info.UUID == uuidA2 {
			listedPrivate = info.Private
		}
	}
	if !listedPrivate {
		t.Fatalf("private secret should be listed via metadata with Private=true")
	}
}

func TestEphemeralLifecycleAcrossRestart(t *testing.T) {
	r := newTestRegistry(t)
	uuidRef := SecretRef{Kind: RefUUID, UUID: uuidEph}
	usageRef := SecretRef{Kind: RefUsage, Usage: "temp-key"}

	if _, err := r.Resolve(uuidRef, tenantA); err != nil {
		t.Fatalf("ephemeral secret should be live before restart, got %v", err)
	}
	for _, info := range r.PersistableSecrets() {
		if info.Ephemeral {
			t.Fatalf("ephemeral secret must not be persisted: %+v", info)
		}
	}

	r.Restart()

	if _, err := r.Resolve(uuidRef, tenantA); !errors.Is(err, ErrEphemeralUnavailable) {
		t.Fatalf("expected ErrEphemeralUnavailable by uuid, got %v", err)
	}
	if _, err := r.Resolve(usageRef, tenantA); !errors.Is(err, ErrEphemeralUnavailable) {
		t.Fatalf("expected ErrEphemeralUnavailable by usage, got %v", err)
	}

	fragment := []byte(`<domain><devices><disk><auth><secret uuid="` + uuidEph + `"/></auth></disk></devices></domain>`)
	if _, err := r.ValidateDomainSecrets(fragment, tenantA); !errors.Is(err, ErrEphemeralUnavailable) {
		t.Fatalf("domain validation should reject dead ephemeral, got %v", err)
	}

	// Durable secrets survive the restart.
	if _, err := r.Resolve(SecretRef{Kind: RefUUID, UUID: uuidA1}, tenantA); err != nil {
		t.Fatalf("durable secret lost across restart: %v", err)
	}

	// Re-registration after restart revives the ephemeral secret.
	mustRegister(t, r, Secret{UUID: uuidEph, Usage: "temp-key", TenantID: tenantA, Ephemeral: true, Value: "new-ephemeral"})
	if _, err := r.Resolve(uuidRef, tenantA); err != nil {
		t.Fatalf("re-registered ephemeral should be live, got %v", err)
	}
}

func TestDomainUUIDDoesNotActAsSecretUUID(t *testing.T) {
	r := newTestRegistry(t)

	// Domain UUID equals another tenant's secret UUID. With no <secret> ref
	// there is nothing to authorize; the equal UUID must not be confused.
	noRef := []byte(`<domain><uuid>` + uuidB1 + `</uuid><devices><disk><target dev="vda"/></disk></devices></domain>`)
	refs, err := r.ValidateDomainSecrets(noRef, tenantA)
	if err != nil {
		t.Fatalf("domain UUID must not be treated as a secret reference: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no references, got %d", len(refs))
	}

	// An explicit <secret> using the same UUID is still ownership-checked.
	bad := []byte(`<domain><uuid>` + uuidB1 + `</uuid><devices><disk><auth><secret uuid="` + uuidB1 + `"/></auth></disk></devices></domain>`)
	if _, err := r.ValidateDomainSecrets(bad, tenantA); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("explicit secret ref must still be ownership-checked, got %v", err)
	}
}

func TestMalformedReferences(t *testing.T) {
	r := newTestRegistry(t)
	cases := map[string]string{
		"both identifiers":   `<domain><devices><disk><auth><secret uuid="` + uuidA1 + `" usage="ceph-rbd"/></auth></disk></devices></domain>`,
		"neither identifier": `<domain><devices><disk><auth><secret type="ceph"/></auth></disk></devices></domain>`,
		"malformed uuid":     `<domain><devices><disk><auth><secret uuid="not-a-uuid"/></auth></disk></devices></domain>`,
	}
	for name, fragment := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := r.ValidateDomainSecrets([]byte(fragment), tenantA); !errors.Is(err, ErrInvalidReference) {
				t.Fatalf("expected ErrInvalidReference, got %v", err)
			}
		})
	}

	if _, err := r.ValidateDomainSecrets([]byte(`<domain><disk`), tenantA); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("expected ErrMalformedXML, got %v", err)
	}
}

func TestParseDomainXML_CollectsAuthAndEncryption(t *testing.T) {
	domainUUID, refs, err := ParseDomainXML([]byte(`
<domain>
  <uuid>77777777-7777-7777-7777-777777777777</uuid>
  <devices>
    <disk>
      <auth username="u"><secret type="ceph" uuid="` + uuidA1 + `"/></auth>
      <encryption format="luks"><secret type="luks" usage="luks-vault"/></encryption>
    </disk>
  </devices>
</domain>`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if domainUUID != "77777777-7777-7777-7777-777777777777" {
		t.Fatalf("domain UUID not parsed: %q", domainUUID)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].Kind != RefUUID || refs[0].Path != "disk[0]/auth/secret" || refs[0].Type != "ceph" {
		t.Fatalf("bad auth ref: %+v", refs[0])
	}
	if refs[1].Kind != RefUsage || refs[1].Path != "disk[0]/encryption/secret" || refs[1].Type != "luks" {
		t.Fatalf("bad encryption ref: %+v", refs[1])
	}
}

func TestRegisterValidation(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Secret{UUID: "bad", TenantID: tenantA}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("bad uuid should fail, got %v", err)
	}
	if err := r.Register(Secret{UUID: uuidA1, TenantID: "  "}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("empty tenant should fail, got %v", err)
	}
	mustRegister(t, r, Secret{UUID: uuidA1, TenantID: tenantA})
	if err := r.Register(Secret{UUID: uuidA1, TenantID: tenantA}); !errors.Is(err, ErrSecretAlreadyRegistered) {
		t.Fatalf("duplicate should fail, got %v", err)
	}
}
