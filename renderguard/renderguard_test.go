package renderguard

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

const testSecret = "s3cr3t-token-9f27"

func lit(value string) CallArg {
	return CallArg{Value: value, Literal: true}
}

func dyn() CallArg {
	return CallArg{Literal: false}
}

func testSecrets(namespace string) (map[string][]string, error) {
	if namespace != "default" {
		return map[string][]string{}, nil
	}
	return map[string][]string{
		"db-credentials": {testSecret, "short"},
	}, nil
}

func findReason(dec Decision, reason Reason) *Finding {
	for i := range dec.Findings {
		if dec.Findings[i].Reason == reason {
			return &dec.Findings[i]
		}
	}
	return nil
}

func TestLookupSecretRejected(t *testing.T) {
	dec, err := Evaluate(Input{
		Calls: []FunctionCall{{
			Name: "lookup",
			Args: []CallArg{lit("v1"), lit("Secret"), lit("default"), lit("db-credentials")},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed {
		t.Fatal("expected lookup of Secret to be rejected")
	}
	if findReason(dec, ReasonLookupSecret) == nil {
		t.Fatalf("expected %q finding, got %+v", ReasonLookupSecret, dec.Findings)
	}
}

func TestLookupDynamicKindRejected(t *testing.T) {
	dec, err := Evaluate(Input{
		Calls: []FunctionCall{{
			Name: "lookup",
			Args: []CallArg{lit("v1"), dyn(), lit("default"), lit("whatever")},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed {
		t.Fatal("expected lookup with dynamic kind to be rejected")
	}
	if findReason(dec, ReasonLookupKindUnknown) == nil {
		t.Fatalf("expected %q finding, got %+v", ReasonLookupKindUnknown, dec.Findings)
	}
}

func TestDangerousFunctionsRejected(t *testing.T) {
	for _, name := range []string{"getHostByName", "env", "expandenv", "readFile"} {
		dec, err := Evaluate(Input{
			Calls: []FunctionCall{{Name: name, Args: []CallArg{lit("x")}}},
		}, testSecrets)
		if err != nil {
			t.Fatal(err)
		}
		if dec.Allowed {
			t.Fatalf("expected function %q to be rejected", name)
		}
		if findReason(dec, ReasonDangerousFunction) == nil {
			t.Fatalf("expected %q finding for %q, got %+v", ReasonDangerousFunction, name, dec.Findings)
		}
	}
}

func TestConfigMapBase64LeakRejected(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(testSecret))
	dec, err := Evaluate(Input{
		Resources: []Resource{{
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "app-config",
			Fields: []StringField{
				{Path: "data.app.yaml", Value: "token: " + encoded + "\n"},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed {
		t.Fatal("expected ConfigMap with base64-encoded secret to be rejected")
	}
	f := findReason(dec, ReasonSecretValueLeak)
	if f == nil {
		t.Fatalf("expected %q finding, got %+v", ReasonSecretValueLeak, dec.Findings)
	}
	if f.Kind != "ConfigMap" || f.Namespace != "default" || f.Name != "app-config" {
		t.Fatalf("finding does not identify the resource: %+v", f)
	}
	if f.Field != "data.app.yaml" {
		t.Fatalf("finding does not report the field path: %+v", f)
	}
}

func TestHexLeakInAnnotationRejected(t *testing.T) {
	hexed := hex.EncodeToString([]byte(testSecret))
	dec, err := Evaluate(Input{
		Resources: []Resource{{
			Kind:      "Deployment",
			Namespace: "default",
			Name:      "web",
			Fields: []StringField{
				{Path: "metadata.annotations.checksum", Value: hexed},
				{Path: "spec.template.spec.containers[0].image", Value: "nginx:1.27"},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed {
		t.Fatal("expected hex-encoded secret in annotation to be rejected")
	}
	f := findReason(dec, ReasonSecretValueLeak)
	if f == nil || f.Field != "metadata.annotations.checksum" {
		t.Fatalf("expected leak finding on annotation field, got %+v", dec.Findings)
	}
}

func TestPlainSecretLeakRejected(t *testing.T) {
	dec, err := Evaluate(Input{
		Resources: []Resource{{
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "leaky",
			Fields: []StringField{
				{Path: "metadata.labels.note", Value: "prefix-" + testSecret + "-suffix"},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed {
		t.Fatal("expected plain secret substring in label to be rejected")
	}
}

func TestLookupDeploymentAllowed(t *testing.T) {
	dec, err := Evaluate(Input{
		Calls: []FunctionCall{{
			Name: "lookup",
			Args: []CallArg{lit("apps/v1"), lit("Deployment"), lit("default"), lit("web")},
		}},
		Resources: []Resource{{
			Kind:      "Deployment",
			Namespace: "default",
			Name:      "web",
			Fields: []StringField{
				{Path: "spec.template.spec.containers[0].image", Value: "nginx:1.27"},
				{Path: "metadata.labels.app", Value: "web"},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Allowed {
		t.Fatalf("expected clean render to be allowed, got %+v", dec.Findings)
	}
}

func TestShortSecretValueIgnored(t *testing.T) {
	dec, err := Evaluate(Input{
		Resources: []Resource{{
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "tiny",
			Fields: []StringField{
				{Path: "data.note", Value: "this field mentions short literally"},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Allowed {
		t.Fatalf("expected short secret value to be ignored, got %+v", dec.Findings)
	}
}

func TestSecretResourcesNotScanned(t *testing.T) {
	dec, err := Evaluate(Input{
		Resources: []Resource{{
			Kind:      "Secret",
			Namespace: "default",
			Name:      "db-credentials",
			Fields: []StringField{
				{Path: "data.token", Value: base64.StdEncoding.EncodeToString([]byte(testSecret))},
			},
		}},
	}, testSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Allowed {
		t.Fatalf("expected Secret resources to be skipped, got %+v", dec.Findings)
	}
}
