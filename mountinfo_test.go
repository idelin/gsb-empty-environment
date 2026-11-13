package netsec

import (
	"reflect"
	"strings"
	"testing"
)

const sampleMountInfo = `36 35 0:29 / / rw,relatime master:12 - overlay overlay rw
41 36 0:55 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
42 36 0:61 / /dev rw - tmpfs tmpfs rw
43 36 0:67 / /var/run/secrets/kubernetes.io/serviceaccount ro,relatime - tmpfs kube-api rw
44 36 0:68 / /mnt/data rw,relatime shared:7 - ext4 /dev/sdb1 rw
45 44 0:69 / /mnt/data/nested rw,relatime - ext4 /dev/sdc1 rw
46 36 0:70 / /mnt/team\040creds rw,relatime - tmpfs team-creds rw
47 36 0:71 / /mnt/with\011tab rw,relatime shared:9 master:3 - tmpfs tabs rw
48 36 0:72 / /mnt/with\012newline rw,relatime - tmpfs newlines rw
49 36 0:73 / /mnt/back\134slash rw,relatime - tmpfs backs rw
`

func TestParseMountPoints(t *testing.T) {
	got, err := ParseMountPoints(strings.NewReader(sampleMountInfo))
	if err != nil {
		t.Fatalf("ParseMountPoints returned error: %v", err)
	}

	want := []string{
		"/",
		"/proc",
		"/dev",
		"/var/run/secrets/kubernetes.io/serviceaccount",
		"/mnt/data",
		"/mnt/data/nested",
		"/mnt/team creds",
		"/mnt/with\ttab",
		"/mnt/with\nnewline",
		"/mnt/back\\slash",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount points mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestParseMountPointsDedupesAndSkipsBlank(t *testing.T) {
	text := strings.Join([]string{
		"",
		"43 36 0:67 / /run/secrets ro - tmpfs a rw",
		"  ",
		"50 36 0:70 / /run/secrets rw - tmpfs b rw",
	}, "\n")

	got, err := ParseMountPoints(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseMountPoints returned error: %v", err)
	}
	want := []string{"/run/secrets"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseMountPointsErrors(t *testing.T) {
	t.Run("missing separator", func(t *testing.T) {
		if _, err := ParseMountPoints(strings.NewReader("43 36 0:67 / /run/secrets ro")); err == nil {
			t.Fatal("expected error for record without \"-\" separator")
		}
	})

	t.Run("too few leading fields", func(t *testing.T) {
		if _, err := ParseMountPoints(strings.NewReader("43 36 0:67 / - tmpfs a rw")); err == nil {
			t.Fatal("expected error for malformed record")
		}
	})

	t.Run("bad octal escape", func(t *testing.T) {
		text := "46 36 0:70 / /mnt/bad\\04x rw - tmpfs a rw"
		if _, err := ParseMountPoints(strings.NewReader(text)); err == nil {
			t.Fatal("expected error for invalid octal escape")
		}
	})

	t.Run("truncated octal escape", func(t *testing.T) {
		text := "46 36 0:70 / /mnt/bad\\04 rw - tmpfs a rw"
		if _, err := ParseMountPoints(strings.NewReader(text)); err == nil {
			t.Fatal("expected error for truncated octal escape")
		}
	})

	t.Run("relative mount point", func(t *testing.T) {
		text := "46 36 0:70 / rel/path rw - tmpfs a rw"
		if _, err := ParseMountPoints(strings.NewReader(text)); err == nil {
			t.Fatal("expected error for relative mount point")
		}
	})
}

func TestUnescapeOctal(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain/path", "plain/path"},
		{"/mnt/team\\040creds", "/mnt/team creds"},
		{"/a\\011b\\012c\\134d", "/a\tb\nc\\d"},
		{"/\\040\\040", "/  "},
	}

	for _, tc := range cases {
		got, err := unescapeOctal(tc.in)
		if err != nil {
			t.Fatalf("unescapeOctal(%q) returned error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("unescapeOctal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
