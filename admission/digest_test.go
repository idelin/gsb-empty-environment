package admission

import "testing"

func TestDigestValidate(t *testing.T) {
	valid := Digest{Hash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 42}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}

	cases := []struct {
		name string
		d    Digest
	}{
		{"uppercase hex", Digest{Hash: "A123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 1}},
		{"too short", Digest{Hash: "abcdef", Size: 1}},
		{"too long", Digest{Hash: valid.Hash + "00", Size: 1}},
		{"non hex char", Digest{Hash: "g23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 1}},
		{"negative size", Digest{Hash: valid.Hash, Size: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.d.Validate(); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			} else if CodeOf(err) != CodeInvalidArgument {
				t.Fatalf("code = %s, want invalid_argument", CodeOf(err))
			}
		})
	}
}

func TestParseResource(t *testing.T) {
	h := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("cas blobs path", func(t *testing.T) {
		r, err := ParseResource("instance/x/blobs/" + h + "/128")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if r.Instance != "instance/x" || r.Upload {
			t.Fatalf("unexpected resource: %+v", r)
		}
		if r.Digest.Hash != h || r.Digest.Size != 128 {
			t.Fatalf("digest mismatch: %+v", r.Digest)
		}
	})

	t.Run("absolute leading slash", func(t *testing.T) {
		r, err := ParseResource("/blobs/" + h + "/0")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if r.Instance != "" || r.Digest.Size != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})

	t.Run("bytestream upload query", func(t *testing.T) {
		r, err := ParseResource("inst/blobs/uploads/u-1?hash=" + h + "&size_bytes=7")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !r.Upload || r.Digest.Hash != h || r.Digest.Size != 7 {
			t.Fatalf("unexpected upload resource: %+v", r)
		}
	})

	bad := []string{
		"",
		"inst/blobs/uploads/u-1",
		"inst/blobs/uploads/u-1?hash=" + h,
		"inst/blobs/uploads/u-1?hash=" + h + "&size_bytes=abc",
		"inst/blobs/" + h + "/notanint",
		"inst/blobs/short/5",
		"inst/blobs/" + "G" + h[1:] + "/5",
		"no-blobs-here",
	}
	for _, name := range bad {
		t.Run("bad/"+name, func(t *testing.T) {
			if _, err := ParseResource(name); err == nil {
				t.Fatalf("expected parse error for %q", name)
			} else if CodeOf(err) != CodeInvalidArgument {
				t.Fatalf("code = %s for %q", CodeOf(err), name)
			}
		})
	}
}
