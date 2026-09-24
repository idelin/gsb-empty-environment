package canonicalkey

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalCoreDistinctions(t *testing.T) {
	base := func(params Params) Request {
		return Request{
			Tenant:        "tenant-a",
			ParameterType: "search",
			ConfigVersion: PresentString("cfg-3"),
			Params:        params,
		}
	}

	tests := []struct {
		name      string
		params    Params
		want      string
		wantError error
	}{
		{name: "number one", params: PresentParams(Number("1")), want: "K1T8:tenant-aY6:searchV5:cfg-3PN1:1"},
		{name: "string one", params: PresentParams("1"), want: "K1T8:tenant-aY6:searchV5:cfg-3PS1:1"},
		{name: "missing params", params: AbsentParams(), want: "K1T8:tenant-aY6:searchV5:cfg-3PX"},
		{name: "null params", params: NullParams(), want: "K1T8:tenant-aY6:searchV5:cfg-3PZ"},
		{name: "null config", params: PresentParams(Number("1")), wantError: ErrNullConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := base(tt.params)
			if tt.wantError != nil {
				request.ConfigVersion = NullString()
			}
			got, err := CanonicalRequest(request, DefaultLimits())
			if tt.wantError != nil {
				if !errors.Is(err, tt.wantError) {
					t.Fatalf("error = %v, want %v", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalRequest returned error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("canonical = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestObjectOrderAndArrayOrder(t *testing.T) {
	request := Request{
		Tenant:        "tenant-a",
		ParameterType: "search",
		ConfigVersion: PresentString("cfg-3"),
	}
	left := request
	left.Params = PresentParams(Object{"a": Number("1"), "b": Number("2")})
	right := request
	right.Params = PresentParams(Object{"b": Number("2"), "a": Number("1")})
	leftCanonical, err := CanonicalRequest(left, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	rightCanonical, err := CanonicalRequest(right, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if string(leftCanonical) != string(rightCanonical) {
		t.Fatalf("object field order changed canonical bytes:\n%s\n%s", leftCanonical, rightCanonical)
	}

	left.Params = PresentParams(Array{Number("1"), Number("2")})
	right.Params = PresentParams(Array{Number("2"), Number("1")})
	leftCanonical, _ = CanonicalRequest(left, DefaultLimits())
	rightCanonical, _ = CanonicalRequest(right, DefaultLimits())
	if string(leftCanonical) == string(rightCanonical) {
		t.Fatal("array order was incorrectly ignored")
	}
}

func TestExactNumberCanonicalization(t *testing.T) {
	request := Request{"tenant-a", "search", PresentString("cfg-3"), AbsentParams()}
	equal := []string{"1", "1.0", "01", "1e0", "1.000", "0001.0000"}
	var first []byte
	for index, raw := range equal {
		request.Params = PresentParams(Number(raw))
		canonical, err := CanonicalRequest(request, DefaultLimits())
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if index == 0 {
			first = canonical
			continue
		}
		if string(canonical) != string(first) {
			t.Fatalf("%s canonicalized to %q, want %q", raw, canonical, first)
		}
	}

	request.Params = PresentParams(Number("9007199254740993"))
	large, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(large), "N16:9007199254740993") {
		t.Fatalf("large number lost precision: %s", large)
	}

	request.Params = PresentParams(Number("1e999999999"))
	_, err = CanonicalRequest(request, DefaultLimits())
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("huge exponent error = %v, want %v", err, ErrBudgetExceeded)
	}
}

func TestUnicodeIsNotNormalizedAndSortsByUTF8(t *testing.T) {
	request := Request{"tenant-a", "search", PresentString("cfg-3"), AbsentParams()}
	nfc := string([]byte{0xC3, 0xA9})
	nfd := string([]byte{0x65, 0xCC, 0x81})
	request.Params = PresentParams(Object{nfc: Number("1"), nfd: Number("2"), "a": Number("3")})
	canonical, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	text := string(canonical)
	aAt := strings.Index(text, "S1:a")
	nfdAt := strings.Index(text, "S3:"+nfd)
	nfcAt := strings.Index(text, "S2:"+nfc)
	if !(aAt < nfdAt && nfdAt < nfcAt) {
		t.Fatalf("fields not sorted by UTF-8 bytes: %s", text)
	}

	request.Params = PresentParams(nfc)
	nfcCanonical, _ := CanonicalRequest(request, DefaultLimits())
	request.Params = PresentParams(nfd)
	nfdCanonical, _ := CanonicalRequest(request, DefaultLimits())
	if string(nfcCanonical) == string(nfdCanonical) {
		t.Fatal("Unicode normalization unexpectedly made distinct bytes equivalent")
	}

	request.Params = PresentParams("\xff")
	_, err = CanonicalRequest(request, DefaultLimits())
	if !errors.Is(err, ErrInvalidUnicode) {
		t.Fatalf("invalid UTF-8 error = %v, want %v", err, ErrInvalidUnicode)
	}
}

func TestDecodeStrictnessAndOmittedConfig(t *testing.T) {
	valid := `{"tenant":"tenant-a","parameter_type":"search","config_version":"cfg-3","params":{"a":1}}`
	request, err := DecodeRequest([]byte(valid), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !request.ConfigVersion.Present || request.ConfigVersion.Null || request.ConfigVersion.Value != "cfg-3" {
		t.Fatalf("config version decoded incorrectly: %#v", request.ConfigVersion)
	}
	if _, err := CanonicalRequest(request, DefaultLimits()); err != nil {
		t.Fatal(err)
	}

	invalid := []struct {
		name string
		json string
		want error
	}{
		{name: "duplicate field", json: `{"tenant":"a","tenant":"b","parameter_type":"p","config_version":"v"}`, want: ErrDuplicateField},
		{name: "unknown field", json: `{"tenant":"a","parameter_type":"p","config_version":"v","unexpected":true}`, want: ErrUnknownField},
		{name: "missing config", json: `{"tenant":"a","parameter_type":"p","params":null}`, want: ErrMissingConfig},
		{name: "numeric versus string", json: `{"tenant":"a","parameter_type":"p","config_version":"v","params":"1"}`, want: nil},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			request, err := DecodeRequest([]byte(tt.json), DefaultLimits())
			if tt.name == "numeric versus string" {
				if err != nil {
					t.Fatal(err)
				}
				numberRequest, err := DecodeRequest([]byte(strings.Replace(tt.json, `"params":"1"`, `"params":1`, 1)), DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				left, _ := CanonicalRequest(request, DefaultLimits())
				right, _ := CanonicalRequest(numberRequest, DefaultLimits())
				if string(left) == string(right) {
					t.Fatal("JSON number and JSON string collided")
				}
				return
			}
			if err != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("decode error = %v, want %v", err, tt.want)
				}
				return
			}
			_, err = CanonicalRequest(request, DefaultLimits())
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestJSONRejectsInvalidUTF8(t *testing.T) {
	data := []byte(`{"tenant":"a","parameter_type":"p","config_version":"v","params":"` + "\xff" + `"}`)
	_, err := DecodeRequest(data, DefaultLimits())
	if !errors.Is(err, ErrInvalidUnicode) {
		t.Fatalf("invalid JSON UTF-8 error = %v, want %v", err, ErrInvalidUnicode)
	}
}
