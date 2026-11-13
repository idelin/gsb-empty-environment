package netsec

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func staticArg(value string) Arg  { return Arg{Value: value, Static: true} }
func dynamicArg(value string) Arg { return Arg{Value: value, Static: false} }

func lookupCall(kind Arg) FuncCall {
	return FuncCall{
		Name:     "lookup",
		Args:     []Arg{staticArg("v1"), kind, staticArg("default"), staticArg("db")},
		Position: "templates/secret.yaml:8:14",
	}
}

func secretFetcher(t *testing.T, value []byte) SecretFetcher {
	t.Helper()
	return func() ([]SecretValue, error) {
		return []SecretValue{{
			Namespace: "default",
			Name:      "db-credentials",
			Key:       "password",
			Value:     value,
		}}, nil
	}
}

func TestLookupSecretRejected(t *testing.T) {
	calls := []FuncCall{lookupCall(staticArg("Secret"))}

	report, err := Admit(calls, nil, nil, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Allowed || len(report.Violations) != 1 {
		t.Fatalf("expected one rejection, got allowed=%v violations=%+v", report.Allowed, report.Violations)
	}
	violation := report.Violations[0]
	if violation.Kind != ViolationLookupSecret || violation.Function != "lookup" {
		t.Fatalf("unexpected violation: %+v", violation)
	}
	if violation.Position != "templates/secret.yaml:8:14" {
		t.Fatalf("template position not reported: %+v", violation)
	}
}

func TestDynamicLookupKindRejected(t *testing.T) {
	cases := []struct {
		name string
		call FuncCall
	}{
		{"concatenation", lookupCall(dynamicArg(`printf "%s" .Values.kind`))},
		{"pipeline", lookupCall(dynamicArg(".Values.resourceKind | toString"))},
		{"missing argument", FuncCall{
			Name:     "lookup",
			Args:     []Arg{staticArg("v1")},
			Position: "templates/x.yaml:1:1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := Admit([]FuncCall{tc.call}, nil, nil, Options{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if report.Allowed || len(report.Violations) != 1 ||
				report.Violations[0].Kind != ViolationDynamicLookup {
				t.Fatalf("expected dynamic lookup rejection, got %+v", report.Violations)
			}
		})
	}
}

func TestConfigMapBase64SubstringRejected(t *testing.T) {
	password := []byte("supersecret-pw-2026")
	encoded := base64.StdEncoding.EncodeToString(password)
	resources := []Resource{{
		Kind:      "ConfigMap",
		Namespace: "default",
		Name:      "app-config",
		Fields: []FieldValue{{
			Path:  "data.connection-string",
			Value: "postgres://user:" + encoded + "@db:5432/app",
		}},
	}}

	report, err := Admit(nil, resources, secretFetcher(t, password), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Allowed || len(report.Violations) != 1 {
		t.Fatalf("expected one leak, got allowed=%v %+v", report.Allowed, report.Violations)
	}
	violation := report.Violations[0]
	if violation.Kind != ViolationSecretLeak || violation.Encoding != EncodingBase64 {
		t.Fatalf("unexpected violation: %+v", violation)
	}
	if violation.ResourceKind != "ConfigMap" || violation.Namespace != "default" ||
		violation.ResourceName != "app-config" || violation.FieldPath != "data.connection-string" {
		t.Fatalf("resource/path not reported: %+v", violation)
	}
	if violation.SecretName != "db-credentials" || violation.SecretKey != "password" {
		t.Fatalf("secret coordinates not reported: %+v", violation)
	}
}

func TestHexSubstringRejected(t *testing.T) {
	password := []byte("hex-secret-token")
	lower := hex.EncodeToString(password)
	upper := strings.ToUpper(lower)
	resources := []Resource{
		{
			Kind: "ConfigMap",
			Name: "lower-cm",
			Fields: []FieldValue{{
				Path:  "data.checksum",
				Value: "prefix-" + lower + "-suffix",
			}},
		},
		{
			Kind: "ConfigMap",
			Name: "upper-cm",
			Fields: []FieldValue{{
				Path:  "metadata.annotations.rollout",
				Value: upper,
			}},
		},
	}

	report, err := Admit(nil, resources, secretFetcher(t, password), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Allowed || len(report.Violations) != 2 {
		t.Fatalf("expected two hex leaks, got %+v", report.Violations)
	}
	for _, violation := range report.Violations {
		if violation.Kind != ViolationSecretLeak || violation.Encoding != EncodingHex {
			t.Fatalf("expected hex leak, got %+v", violation)
		}
	}
}

func TestLegalLookupDeploymentAllowed(t *testing.T) {
	calls := []FuncCall{
		lookupCall(staticArg("Deployment")),
		{
			Name:     "lookup",
			Args:     []Arg{staticArg("apps/v1"), staticArg("ConfigMap"), staticArg("ns"), staticArg("cfg")},
			Position: "templates/deploy.yaml:2:1",
		},
	}
	resources := []Resource{{
		Kind:      "ConfigMap",
		Namespace: "default",
		Name:      "safe-config",
		Fields: []FieldValue{{
			Path:  "data.message",
			Value: "nothing sensitive here",
		}},
	}}

	report, err := Admit(calls, resources, secretFetcher(t, []byte("supersecret-pw-2026")), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Allowed || len(report.Violations) != 0 {
		t.Fatalf("expected admission, got %+v", report.Violations)
	}
}

func TestForbiddenExternalStateFunctions(t *testing.T) {
	for _, name := range []string{"getHostByName", "env", "expandenv", "readFile"} {
		t.Run(name, func(t *testing.T) {
			report, err := Admit([]FuncCall{{Name: name, Args: []Arg{staticArg("x")}}}, nil, nil, Options{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if report.Allowed || report.Violations[0].Kind != ViolationForbiddenFunction {
				t.Fatalf("expected forbidden function rejection, got %+v", report.Violations)
			}
		})
	}
}

func TestPlainTextAndLabelsScanned(t *testing.T) {
	password := []byte("supersecret-pw-2026")
	resources := []Resource{{
		Kind: "Deployment",
		Name: "web",
		Fields: []FieldValue{
			{Path: "metadata.annotations.checksum", Value: "v2:" + string(password)},
			{Path: "metadata.labels.team", Value: "platform"},
		},
	}}

	report, err := Admit(nil, resources, secretFetcher(t, password), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Allowed || len(report.Violations) != 1 ||
		report.Violations[0].Encoding != EncodingPlain ||
		report.Violations[0].FieldPath != "metadata.annotations.checksum" {
		t.Fatalf("expected plaintext annotation leak, got %+v", report.Violations)
	}
}

func TestShortSecretIgnoredAndSecretKindSkipped(t *testing.T) {
	short := []byte("abc")
	resources := []Resource{
		{
			Kind: "ConfigMap",
			Name: "cm",
			Fields: []FieldValue{{
				Path:  "data.token",
				Value: base64.StdEncoding.EncodeToString(short),
			}},
		},
		{
			Kind: "Secret",
			Name: "actual-secret",
			Fields: []FieldValue{{
				Path:  "data.password",
				Value: base64.StdEncoding.EncodeToString([]byte("supersecret-pw-2026")),
			}},
		},
	}

	report, err := Admit(nil, resources, secretFetcher(t, short), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Allowed {
		t.Fatalf("expected admission for short secret and Secret kind, got %+v", report.Violations)
	}
}

func TestFetcherFailureFailsClosed(t *testing.T) {
	resources := []Resource{{Kind: "ConfigMap", Name: "cm"}}

	if _, err := Admit(nil, resources, nil, Options{}); err == nil {
		t.Fatal("expected error when fetcher is nil")
	}

	failing := func() ([]SecretValue, error) {
		return nil, errors.New("apiserver unavailable")
	}
	if _, err := Admit(nil, resources, failing, Options{}); err == nil ||
		!strings.Contains(err.Error(), "apiserver unavailable") {
		t.Fatalf("expected fetcher error to propagate, got %v", err)
	}
}

func TestFlattenAndNewResource(t *testing.T) {
	object := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "cm",
			"namespace": "team-a",
			"labels":    map[string]any{"app": "demo"},
		},
		"data": map[string]any{
			"config": "value",
		},
		"items": []any{
			map[string]any{"image": "registry.example/app:1"},
			42,
		},
	}

	resource := NewResource(object)
	if resource.Kind != "ConfigMap" || resource.Namespace != "team-a" || resource.Name != "cm" {
		t.Fatalf("coordinates not extracted: %+v", resource)
	}
	paths := map[string]string{}
	for _, field := range resource.Fields {
		paths[field.Path] = field.Value
	}
	expected := map[string]string{
		"apiVersion":          "v1",
		"metadata.labels.app": "demo",
		"metadata.name":       "cm",
		"metadata.namespace":  "team-a",
		"data.config":         "value",
		"items[0].image":      "registry.example/app:1",
	}
	for path, value := range expected {
		if paths[path] != value {
			t.Fatalf("path %q = %q, want %q", path, paths[path], value)
		}
	}
	if _, ok := paths["items[1]"]; ok {
		t.Fatalf("non-string scalar must not produce a field: %+v", paths)
	}
}

func TestEncodedFieldDecodingDefense(t *testing.T) {
	password := []byte("supersecret-pw-2026")

	// Field is entirely base64 of surrounding text that embeds the raw
	// secret, so the base64 needle is offset and not boundary-aligned.
	inner := "password=" + string(password)
	field := base64.StdEncoding.EncodeToString([]byte(inner))
	resources := []Resource{{
		Kind:   "ConfigMap",
		Name:   "cm",
		Fields: []FieldValue{{Path: "data.blob", Value: field}},
	}}

	report, err := Admit(nil, resources, secretFetcher(t, password), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Allowed || len(report.Violations) == 0 {
		t.Fatalf("expected decoded-field detection, got %+v", report.Violations)
	}
}
