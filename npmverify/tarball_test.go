package npmverify

import (
	"errors"
	"testing"
)

func TestTarballManifestNpmLayout(t *testing.T) {
	tarball := makeTarball(t, "acme-lib", "3.1.4")

	manifest, err := TarballManifest(tarball)
	if err != nil {
		t.Fatalf("TarballManifest: %v", err)
	}
	if manifest.Name != "acme-lib" || manifest.Version != "3.1.4" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestTarballManifestRootLayout(t *testing.T) {
	tarball := makeTarballWith(t, map[string]string{
		"package.json": `{"name":"root-lib","version":"0.1.0"}`,
		"index.js":     "// root layout\n",
	})

	manifest, err := TarballManifest(tarball)
	if err != nil {
		t.Fatalf("TarballManifest: %v", err)
	}
	if manifest.Name != "root-lib" || manifest.Version != "0.1.0" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestTarballManifestIgnoresNestedPackageJSON(t *testing.T) {
	tarball := makeTarballWith(t, map[string]string{
		"package/node_modules/evil/package.json": `{"name":"evil","version":"9.9.9"}`,
	})

	if _, err := TarballManifest(tarball); !errors.Is(err, ErrMalformedTarball) {
		t.Fatalf("err = %v, want ErrMalformedTarball", err)
	}
}

func TestTarballManifestErrors(t *testing.T) {
	if _, err := TarballManifest([]byte("not gzip")); !errors.Is(err, ErrMalformedTarball) {
		t.Fatalf("not gzip: %v", err)
	}

	noManifest := makeTarballWith(t, map[string]string{
		"package/index.js": "// no package.json\n",
	})
	if _, err := TarballManifest(noManifest); !errors.Is(err, ErrMalformedTarball) {
		t.Fatalf("missing manifest: %v", err)
	}

	badJSON := makeTarballWith(t, map[string]string{
		"package/package.json": `{not valid json`,
	})
	if _, err := TarballManifest(badJSON); !errors.Is(err, ErrMalformedTarball) {
		t.Fatalf("bad json: %v", err)
	}

	emptyIdentity := makeTarballWith(t, map[string]string{
		"package/package.json": `{"name":"","version":""}`,
	})
	if _, err := TarballManifest(emptyIdentity); !errors.Is(err, ErrMalformedTarball) {
		t.Fatalf("empty identity: %v", err)
	}
}
