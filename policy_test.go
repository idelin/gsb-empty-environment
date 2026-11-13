package netsec_test

import (
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0087"
)

func TestExternalURLPolicyRejectsUnsafeURLs(t *testing.T) {
	policy := netsec.NewExternalURLPolicy()
	cases := []struct {
		name string
		url  string
	}{
		{name: "userinfo", url: "https://user:pass@cdn.example.test/blob"},
		{name: "fragment", url: "https://cdn.example.test/blob#fragment"},
		{name: "plain http", url: "http://cdn.example.test/blob"},
		{name: "ipv4 literal", url: "https://127.0.0.1/blob"},
		{name: "ipv6 literal", url: "https://[::1]/blob"},
		{name: "numeric host", url: "https://999.1.1.1/blob"},
		{name: "hex ip literal", url: "https://0x7f.0.0.1/blob"},
		{name: "unregistered port", url: "https://cdn.example.test:8443/blob"},
		{name: "invalid port", url: "https://cdn.example.test:abc/blob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := policy.ValidateURL(tc.url); err == nil {
				t.Fatalf("ValidateURL(%q) succeeded", tc.url)
			} else if !errors.Is(err, netsec.ErrURLPolicy) {
				t.Fatalf("ValidateURL(%q) error = %v, want ErrURLPolicy", tc.url, err)
			}
		})
	}
}

func TestExternalURLPolicyAllowsRegisteredPort(t *testing.T) {
	policy := netsec.NewExternalURLPolicy()
	raw := "https://cdn.example.test:8443/layers/blob"
	if _, err := policy.ValidateURL(raw); err == nil {
		t.Fatal("unregistered port was accepted")
	}
	if err := policy.RegisterOrigin("https://cdn.example.test:8443"); err != nil {
		t.Fatalf("RegisterOrigin: %v", err)
	}
	u, err := policy.ValidateURL(raw)
	if err != nil {
		t.Fatalf("ValidateURL after registration: %v", err)
	}
	if u.Host != "cdn.example.test:8443" {
		t.Fatalf("canonical host = %q", u.Host)
	}
}

func TestValidateRedirectRequiresSameRegistrableDomain(t *testing.T) {
	policy := netsec.NewExternalURLPolicy()
	from, err := policy.ValidateURL("https://cdn1.example.co.uk/one")
	if err != nil {
		t.Fatalf("from URL: %v", err)
	}
	sameSite, err := policy.ValidateURL("https://cdn2.example.co.uk/two")
	if err != nil {
		t.Fatalf("same-site URL: %v", err)
	}
	crossSite, err := policy.ValidateURL("https://cdn2.other.co.uk/three")
	if err != nil {
		t.Fatalf("cross-site URL: %v", err)
	}
	if err := policy.ValidateRedirect(from, sameSite); err != nil {
		t.Fatalf("same-site redirect rejected: %v", err)
	}
	if err := policy.ValidateRedirect(from, crossSite); err == nil {
		t.Fatal("cross-site redirect accepted")
	}
}

func TestRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"cdn.example.test":            "example.test",
		"a.b.example.test":            "example.test",
		"cdn.example.co.uk":           "example.co.uk",
		"a.b.example.co.uk":           "example.co.uk",
		"example.test":                "example.test",
		"localhost":                   "localhost",
		"127.0.0.1":                   "127.0.0.1",
		"registry.example.test":       "example.test",
		"cdn.registry.example.test":   "example.test",
		"evil.registry.example.test":  "example.test",
		"evil.unrelated.example.test": "example.test",
		"foo.github.io":               "foo.github.io",
		"bar.github.io":               "bar.github.io",
		"a.foo.github.io":             "foo.github.io",
	}
	for host, want := range cases {
		if got := netsec.RegistrableDomain(host); got != want {
			t.Errorf("RegistrableDomain(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestBuildLayerPlanURLCountAndFallback(t *testing.T) {
	repo, err := netsec.NewRepository("https://registry.example.test", "team/app")
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	blob := []byte("plan")
	digest := digestOf(blob)
	manifest := manifestFor(repo, "manifest")
	policy := netsec.NewExternalURLPolicy()
	for i := 1; i <= 4; i++ {
		if err := policy.RegisterOrigin("https://cdn" + string(rune('0'+i)) + ".example.test:8443"); err != nil {
			t.Fatalf("RegisterOrigin: %v", err)
		}
	}

	withoutURLs := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType}
	plan, err := netsec.BuildLayerPlan(policy, manifest, withoutURLs)
	if err != nil {
		t.Fatalf("BuildLayerPlan without URLs: %v", err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Kind != netsec.SourceRepository {
		t.Fatalf("no-URL plan = %#v", plan.Sources)
	}
	wantPath, _ := repo.BlobPath(digest)
	if plan.Sources[0].URL.Path != wantPath {
		t.Fatalf("no-URL source path = %q, want %q", plan.Sources[0].URL.Path, wantPath)
	}

	urls := []string{
		"https://cdn1.example.test:8443/1",
		"https://cdn2.example.test:8443/2",
		"https://cdn3.example.test:8443/3",
		"https://cdn4.example.test:8443/4",
	}
	withFour := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: urls}
	plan, err = netsec.BuildLayerPlan(policy, manifest, withFour)
	if err != nil {
		t.Fatalf("BuildLayerPlan with four URLs: %v", err)
	}
	if len(plan.Sources) != 4 {
		t.Fatalf("four-URL plan has %d sources", len(plan.Sources))
	}

	five := append(append([]string(nil), urls...), "https://cdn1.example.test:8443/5")
	tooMany := netsec.Layer{Digest: digest, Size: int64(len(blob)), MediaType: layerMediaType, URLs: five}
	if _, err := netsec.BuildLayerPlan(policy, manifest, tooMany); !errors.Is(err, netsec.ErrTooManyURLs) {
		t.Fatalf("five-URL plan error = %v, want ErrTooManyURLs", err)
	}
}

func TestBuildLayerPlanRejectsUnsafeURLs(t *testing.T) {
	repo, err := netsec.NewRepository("https://registry.example.test", "team/app")
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	blob := []byte("plan")
	manifest := manifestFor(repo, "manifest")
	policy := netsec.NewExternalURLPolicy()
	cases := []string{
		"https://user@cdn.example.test/blob",
		"https://cdn.example.test/blob#fragment",
		"https://127.0.0.1/blob",
		"http://cdn.example.test/blob",
	}
	for _, raw := range cases {
		t.Run(strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			layer := netsec.Layer{Digest: digestOf(blob), Size: int64(len(blob)), MediaType: layerMediaType, URLs: []string{raw}}
			if _, err := netsec.BuildLayerPlan(policy, manifest, layer); err == nil {
				t.Fatalf("BuildLayerPlan accepted %q", raw)
			}
		})
	}
}

func TestRepositoryOriginURLDoesNotNeedExternalPolicy(t *testing.T) {
	repo, err := netsec.NewRepository("https://registry.example.test:8443", "team/app")
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	blob := []byte("mirror plan")
	manifest := manifestFor(repo, "manifest")
	layer := netsec.Layer{
		Digest:    digestOf(blob),
		Size:      int64(len(blob)),
		MediaType: layerMediaType,
		URLs:      []string{"https://registry.example.test:8443/mirror/blob"},
	}
	plan, err := netsec.BuildLayerPlan(nil, manifest, layer)
	if err != nil {
		t.Fatalf("BuildLayerPlan origin URL: %v", err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Kind != netsec.SourceExternal {
		t.Fatalf("origin URL source = %#v, want one external (tokenless) source", plan.Sources)
	}
}
