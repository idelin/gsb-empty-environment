package netsec

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNewRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawURL  string
		want    Request
		wantErr error
	}{
		{
			name:   "https with user and path",
			rawURL: "https://alice@github.com/acme/widgets.git",
			want: Request{
				Protocol: "https",
				Host:     "github.com",
				Path:     "acme/widgets.git",
				Username: "alice",
			},
		},
		{
			name:   "host keeps port",
			rawURL: "https://git.example.com:8443/repo",
			want: Request{
				Protocol: "https",
				Host:     "git.example.com:8443",
				Path:     "repo",
			},
		},
		{
			name:   "ipv6 host keeps brackets",
			rawURL: "https://[2001:db8::1]:443/repo",
			want: Request{
				Protocol: "https",
				Host:     "[2001:db8::1]:443",
				Path:     "repo",
			},
		},
		{
			name:   "benign percent decoding",
			rawURL: "https://user%40domain:pass@host.example.com/path%20name",
			want: Request{
				Protocol: "https",
				Host:     "host.example.com",
				Path:     "path name",
				Username: "user@domain",
			},
		},
		{
			name:    "LF injected via username",
			rawURL:  "https://evil%0ahost=attacker.example%0a@github.com/repo",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "CR injected via username",
			rawURL:  "https://evil%0dhost=attacker.example@github.com/repo",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "NUL injected via username",
			rawURL:  "https://evil%00host=attacker.example@github.com/repo",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "LF injected via path",
			rawURL:  "https://github.com/acme/repo%0ahost=attacker.example",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "NUL injected via path",
			rawURL:  "https://github.com/acme/repo%00.git",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "LF injected via host",
			rawURL:  "https://example.com%0a.evil/repo",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "bare CR in host",
			rawURL:  "https://evil\rexample.com/repo",
			wantErr: ErrUnsafeValue,
		},
		{
			name:    "missing scheme",
			rawURL:  "github.com/acme/repo",
			wantErr: errContains("missing protocol"),
		},
		{
			name:    "missing host",
			rawURL:  "https:///acme/repo",
			wantErr: errContains("missing host"),
		},
		{
			name:    "unparseable URL",
			rawURL:  "://no-scheme",
			wantErr: errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewRequest(tt.rawURL)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("NewRequest(%q) = %+v, want error", tt.rawURL, got)
				}
				if tt.wantErr != errAny && !errors.Is(err, tt.wantErr) && !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Fatalf("NewRequest(%q) error = %v, want %v", tt.rawURL, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRequest(%q) unexpected error: %v", tt.rawURL, err)
			}
			if got != tt.want {
				t.Fatalf("NewRequest(%q) = %+v, want %+v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func errContains(substr string) error {
	return errors.New(substr)
}

// errAny marks a case that must fail for any reason: a parser rejection is
// as safe as an explicit ErrUnsafeValue rejection.
var errAny = errors.New("any non-nil error")

func TestRequestEncode(t *testing.T) {
	t.Parallel()

	req := Request{
		Protocol: "https",
		Host:     "github.com",
		Path:     "acme/widgets.git",
		Username: "alice",
	}
	got, err := req.Encode()
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	want := "protocol=https\nhost=github.com\npath=acme/widgets.git\nusername=alice\n\n"
	if string(got) != want {
		t.Fatalf("Encode() = %q, want %q", got, want)
	}

	var buf bytes.Buffer
	n, err := req.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo unexpected error: %v", err)
	}
	if int(n) != len(want) || buf.String() != want {
		t.Fatalf("WriteTo() = (%d, %q), want (%d, %q)", n, buf.String(), len(want), want)
	}
}

func TestRequestEncodeOmitsEmptyFields(t *testing.T) {
	t.Parallel()

	req := Request{Protocol: "https", Host: "github.com"}
	got, err := req.Encode()
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	want := "protocol=https\nhost=github.com\n\n"
	if string(got) != want {
		t.Fatalf("Encode() = %q, want %q", got, want)
	}
}

func TestRequestEncodeRejectsInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  Request
	}{
		{"LF in host", Request{Protocol: "https", Host: "github.com\nhost=evil"}},
		{"CR in username", Request{Protocol: "https", Host: "github.com", Username: "alice\r"}},
		{"NUL in path", Request{Protocol: "https", Host: "github.com", Path: "repo\x00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.req.Encode(); err == nil {
				t.Fatalf("Encode() = nil error, want rejection for %+v", tt.req)
			}
		})
	}
}

func TestValidateAllFields(t *testing.T) {
	t.Parallel()

	base := Request{Protocol: "https", Host: "github.com", Path: "acme/repo", Username: "alice"}
	tests := []struct {
		name string
		mut  func(*Request)
	}{
		{"protocol", func(r *Request) { r.Protocol = "ht\ntps" }},
		{"host", func(r *Request) { r.Host = "github.com\r" }},
		{"path", func(r *Request) { r.Path = "acme/repo\x00" }},
		{"username", func(r *Request) { r.Username = "alice\n" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := base
			tt.mut(&req)
			err := req.Validate()
			if !errors.Is(err, ErrUnsafeValue) {
				t.Fatalf("Validate() error = %v, want ErrUnsafeValue", err)
			}
			if _, err := req.Encode(); !errors.Is(err, ErrUnsafeValue) {
				t.Fatalf("Encode() error = %v, want ErrUnsafeValue", err)
			}
		})
	}

	empty := Request{}
	if err := empty.Validate(); err != nil {
		t.Fatalf("Validate() on empty request = %v, want nil", err)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestParseResponseReaderError(t *testing.T) {
	t.Parallel()

	req := Request{Protocol: "https", Host: "github.com"}
	if _, err := ParseResponse(req, errReader{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("ParseResponse() error = %v, want io.ErrClosedPipe", err)
	}
}

func TestParseResponse(t *testing.T) {
	t.Parallel()

	req := Request{Protocol: "https", Host: "github.com", Path: "acme/repo", Username: "alice"}

	t.Run("full valid response", func(t *testing.T) {
		t.Parallel()

		wire := "protocol=https\nhost=github.com\nusername=alice\npassword=s3cr=et\n\n"
		cred, err := ParseResponse(req, strings.NewReader(wire))
		if err != nil {
			t.Fatalf("ParseResponse unexpected error: %v", err)
		}
		want := Credential{
			Protocol: "https",
			Host:     "github.com",
			Username: "alice",
			Password: "s3cr=et",
		}
		if cred != want {
			t.Fatalf("ParseResponse() = %+v, want %+v", cred, want)
		}
	})

	t.Run("unsolicited protocol and host ignored", func(t *testing.T) {
		t.Parallel()

		// A partial request that asked for neither protocol nor host.
		partial := Request{Path: "acme/repo", Username: "alice"}
		// A helper that tries to redirect the credential at an attacker host.
		wire := "protocol=http\nhost=attacker.example\nusername=alice\npassword=token\n\n"
		cred, err := ParseResponse(partial, strings.NewReader(wire))
		if err != nil {
			t.Fatalf("ParseResponse unexpected error: %v", err)
		}
		if cred.Host != "" || cred.Protocol != "" {
			t.Fatalf("helper-supplied host/protocol were trusted: %+v", cred)
		}
		if cred.Username != "alice" || cred.Password != "token" {
			t.Fatalf("unexpected credential fields: %+v", cred)
		}
	})

	t.Run("host override ignored without trailing blank line", func(t *testing.T) {
		t.Parallel()

		partial := Request{Username: "alice"}
		wire := "host=attacker.example\npassword=token"
		cred, err := ParseResponse(partial, strings.NewReader(wire))
		if err != nil {
			t.Fatalf("ParseResponse unexpected error at EOF: %v", err)
		}
		if cred.Host != "" {
			t.Fatalf("host was overridden to %q", cred.Host)
		}
		if cred.Password != "token" {
			t.Fatalf("password not parsed: %+v", cred)
		}
	})

	t.Run("unknown keys and path ignored", func(t *testing.T) {
		t.Parallel()

		wire := "path=other/repo\ncapability=autopath\npassword=token\n\n"
		cred, err := ParseResponse(req, strings.NewReader(wire))
		if err != nil {
			t.Fatalf("ParseResponse unexpected error: %v", err)
		}
		if cred.Password != "token" {
			t.Fatalf("password not parsed: %+v", cred)
		}
	})

	t.Run("matching protocol host accepted", func(t *testing.T) {
		t.Parallel()

		wire := "protocol=https\nhost=github.com\npassword=token\n\n"
		if _, err := ParseResponse(req, strings.NewReader(wire)); err != nil {
			t.Fatalf("matching protocol/host rejected: %v", err)
		}
	})

	t.Run("mismatching protocol rejected", func(t *testing.T) {
		t.Parallel()

		// req itself always carries protocol/host, so any divergence is fatal.
		wire := "protocol=http\npassword=token\n\n"
		if _, err := ParseResponse(req, strings.NewReader(wire)); err == nil {
			t.Fatal("ParseResponse accepted mismatching protocol")
		}
	})

	t.Run("mismatching host rejected", func(t *testing.T) {
		t.Parallel()

		wire := "host=attacker.example\npassword=token\n\n"
		if _, err := ParseResponse(req, strings.NewReader(wire)); err == nil {
			t.Fatal("ParseResponse accepted mismatching host")
		}
	})

	t.Run("malformed lines rejected", func(t *testing.T) {
		t.Parallel()

		for _, wire := range []string{
			"novalue\n\n",
			"=emptykey\n\n",
			"password=tok\x00en\n\n",
			"password=tok\ren\n\n",
			"username=ali\nce\n\n",
			"username=ali\x00ce\n\n",
		} {
			if _, err := ParseResponse(req, strings.NewReader(wire)); err == nil {
				t.Fatalf("ParseResponse accepted malformed wire %q", wire)
			}
		}
	})

	t.Run("unsolicited fields with partial request", func(t *testing.T) {
		t.Parallel()

		partial := Request{Path: "acme/repo"}
		wire := "protocol=https\nhost=github.com\npassword=token\n\n"
		cred, err := ParseResponse(partial, strings.NewReader(wire))
		if err != nil {
			t.Fatalf("ParseResponse unexpected error: %v", err)
		}
		if cred.Protocol != "" || cred.Host != "" || cred.Password != "token" {
			t.Fatalf("unexpected credential: %+v", cred)
		}
	})

	t.Run("invalid request rejected", func(t *testing.T) {
		t.Parallel()

		bad := Request{Protocol: "https", Host: "github.com\nhost=evil"}
		if _, err := ParseResponse(bad, strings.NewReader("password=token\n\n")); err == nil {
			t.Fatal("ParseResponse accepted request containing injected newline")
		}
	})
}

func TestInjectionEndToEnd(t *testing.T) {
	t.Parallel()

	// The whole attack chain: a crafted clone URL must never produce a
	// request whose host line points at the attacker.
	rawURL := "https://victim%0ahost=attacker.example%0a@github.com/acme/repo.git"
	if _, err := NewRequest(rawURL); !errors.Is(err, ErrUnsafeValue) {
		t.Fatalf("NewRequest error = %v, want ErrUnsafeValue", err)
	}

	req, err := NewRequest("https://github.com/acme/repo.git")
	if err != nil {
		t.Fatalf("NewRequest unexpected error: %v", err)
	}
	wire, err := req.Encode()
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if !bytes.Contains(wire, []byte("host=github.com\n")) ||
		bytes.Contains(wire, []byte("attacker")) {
		t.Fatalf("encoded request is not pinned to the real host: %q", wire)
	}

	partial := Request{Username: "victim"}
	cred, err := ParseResponse(partial, strings.NewReader(
		"host=attacker.example\nprotocol=http\nusername=victim\npassword=token\n\n"))
	if err != nil {
		t.Fatalf("ParseResponse unexpected error: %v", err)
	}
	if cred.Host != "" || cred.Protocol != "" {
		t.Fatalf("credential escaped to attacker host: %+v", cred)
	}

	// A fully pinned request must reject the same response instead.
	if _, err := ParseResponse(req, strings.NewReader(
		"host=attacker.example\nprotocol=http\npassword=token\n\n")); err == nil {
		t.Fatal("pinned request accepted attacker-controlled host/protocol")
	}
}
