package netsec_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"example.org/netsec/q0131"
)

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyAcceptsIntactImage(t *testing.T) {
	fixture := buildFixture()

	stmt, err := netsec.ParseStatement(fixture.statementRaw)
	requireNoError(t, err)

	err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
	requireNoError(t, err)
}

func TestVerifyWithBlobsAcceptsIntactImage(t *testing.T) {
	fixture := buildFixture()
	stmt, err := netsec.ParseStatement(fixture.statementRaw)
	requireNoError(t, err)

	err = netsec.VerifyWithBlobs(context.Background(), fixture.manifestRaw, stmt, mapGetter(fixture.blobs))
	requireNoError(t, err)
}

func TestVerifyWithBlobsPropagatesFetchFailure(t *testing.T) {
	fixture := buildFixture()
	stmt, err := netsec.ParseStatement(fixture.statementRaw)
	requireNoError(t, err)

	err = netsec.VerifyWithBlobs(context.Background(), fixture.manifestRaw, stmt, mapGetter{})
	requireErrorIs(t, err, netsec.ErrBlobFetch)
}

// Attack family 1: an attestation is replayed from another image, or its
// subject names a layer/config digest instead of the deployed manifest.
func TestSubjectMismatch(t *testing.T) {
	t.Run("replayed attestation from another image", func(t *testing.T) {
		fixture := buildFixture()
		fixture.replaceSubject(digestOf([]byte("some-other-image-manifest")))
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrSubjectMismatch)
	})

	t.Run("subject written as config digest", func(t *testing.T) {
		fixture := buildFixture()
		fixture.replaceSubject(fixture.manifest.Config.Digest)
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrSubjectMismatch)
	})

	t.Run("subject written as a layer digest", func(t *testing.T) {
		fixture := buildFixture()
		fixture.replaceSubject(fixture.manifest.Layers[1].Digest)
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrSubjectMismatch)
	})

	t.Run("subject only offers sha512", func(t *testing.T) {
		fixture := buildFixture()
		fixture.statement.Subject[0].Digest = map[string]string{
			"sha512": "c0ffee",
		}
		fixture.rebuildStatement()

		_, err := netsec.ParseStatement(fixture.statementRaw)
		requireErrorIs(t, err, netsec.ErrBadDigest)
	})

	t.Run("re-serialized manifest with identical semantics", func(t *testing.T) {
		fixture := buildFixture()
		// Re-encoding through a different layout changes bytes, hence the
		// digest, while the document stays semantically identical.
		var indented bytes.Buffer
		if err := jsonIndent(&indented, fixture.manifestRaw); err != nil {
			t.Fatal(err)
		}
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		err = netsec.Verify(indented.Bytes(), stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrSubjectMismatch)
	})
}

// Attack family 2: a layer is dropped, either from the manifest descriptors
// or from the blob set handed to the verifier.
func TestMissingLayer(t *testing.T) {
	t.Run("manifest drops a layer descriptor", func(t *testing.T) {
		fixture := buildFixture()
		fixture.manifest.Layers = fixture.manifest.Layers[:2]
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers[:2])
		requireErrorIs(t, err, netsec.ErrLayerCount)
	})

	t.Run("config drops a diff_id", func(t *testing.T) {
		fixture := buildFixture()
		fixture.config.RootFS.DiffIDs = fixture.config.RootFS.DiffIDs[:2]
		fixture.configRaw = mustMarshal(fixture.config)

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrConfigDigest)
	})

	t.Run("blob set is short one layer", func(t *testing.T) {
		fixture := buildFixture()
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers[:2])
		requireErrorIs(t, err, netsec.ErrLayerCount)
	})

	t.Run("blob getter misses a layer", func(t *testing.T) {
		fixture := buildFixture()
		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)

		delete(fixture.blobs, fixture.manifest.Layers[2].Digest.String())
		err = netsec.VerifyWithBlobs(context.Background(), fixture.manifestRaw, stmt, mapGetter(fixture.blobs))
		requireErrorIs(t, err, netsec.ErrBlobFetch)
	})
}

// Attack family 3: layer order is shuffled. Descriptor order and blob order
// must match rootfs.diff_ids position by position.
func TestLayerOrderSwap(t *testing.T) {
	t.Run("descriptor order swapped, blobs follow descriptors", func(t *testing.T) {
		fixture := buildFixture()
		fixture.manifest.Layers[0], fixture.manifest.Layers[1] =
			fixture.manifest.Layers[1], fixture.manifest.Layers[0]
		fixture.layers[0], fixture.layers[1] = fixture.layers[1], fixture.layers[0]
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrDiffID)
	})

	t.Run("blob order swapped behind unchanged descriptors", func(t *testing.T) {
		fixture := buildFixture()
		fixture.layers[0], fixture.layers[1] = fixture.layers[1], fixture.layers[0]

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrLayerDigest)
	})

	t.Run("diff_id order swapped inside config", func(t *testing.T) {
		fixture := buildFixture()
		fixture.config.RootFS.DiffIDs[0], fixture.config.RootFS.DiffIDs[1] =
			fixture.config.RootFS.DiffIDs[1], fixture.config.RootFS.DiffIDs[0]
		fixture.configRaw = mustMarshal(fixture.config)

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrConfigDigest)
	})
}

// Attack family 4: forged gzip streams. A layer may keep its tar content but
// get re-gzipped, be truncated, or carry garbage under a gzip media type.
func TestForgedGzip(t *testing.T) {
	t.Run("re-gzipped layer with altered tar content", func(t *testing.T) {
		fixture := buildFixture()
		forgedTar := tarLayer("app/bin/server", "#!/bin/sh\necho BACKDOORED\n")
		forged := gzipLayer(forgedTar)

		fixture.layers[1] = forged
		fixture.manifest.Layers[1].Digest = digestOf(forged)
		fixture.manifest.Layers[1].Size = int64(len(forged))
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrDiffID)
	})

	t.Run("truncated gzip stream", func(t *testing.T) {
		fixture := buildFixture()
		forged := fixture.layers[0][:len(fixture.layers[0])-8]

		fixture.layers[0] = forged
		fixture.manifest.Layers[0].Digest = digestOf(forged)
		fixture.manifest.Layers[0].Size = int64(len(forged))
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrDecompress)
	})

	t.Run("plain garbage labeled as gzip", func(t *testing.T) {
		fixture := buildFixture()
		forged := []byte("not a gzip stream at all")

		fixture.layers[0] = forged
		fixture.manifest.Layers[0].Digest = digestOf(forged)
		fixture.manifest.Layers[0].Size = int64(len(forged))
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrDecompress)
	})

	t.Run("bit-flipped gzip body keeps old descriptor", func(t *testing.T) {
		fixture := buildFixture()
		mid := len(fixture.layers[2]) / 2
		fixture.layers[2][mid] ^= 0xff

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		if !errors.Is(err, netsec.ErrLayerDigest) && !errors.Is(err, netsec.ErrDecompress) {
			t.Fatalf("expected ErrLayerDigest or ErrDecompress, got %v", err)
		}
	})
}

func TestConfigAndDescriptorTampering(t *testing.T) {
	t.Run("config blob mutated without descriptor update", func(t *testing.T) {
		fixture := buildFixture()
		fixture.config.OS = "plan9"

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, mustMarshal(fixture.config), fixture.layers)
		requireErrorIs(t, err, netsec.ErrConfigDigest)
	})

	t.Run("declared layer size is wrong", func(t *testing.T) {
		fixture := buildFixture()
		fixture.manifest.Layers[0].Size++
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrLayerSize)
	})

	t.Run("malformed manifest digest", func(t *testing.T) {
		raw := []byte(`{"schemaVersion":2,"config":{"digest":"sha256:nope"},"layers":[]}`)
		_, err := netsec.ParseManifest(raw)
		requireErrorIs(t, err, netsec.ErrBadDigest)
	})

	t.Run("json with trailing data", func(t *testing.T) {
		valid := buildFixture()
		raw := append(append([]byte{}, valid.manifestRaw...), []byte("  null")...)
		_, err := netsec.ParseManifest(raw)
		requireErrorIs(t, err, netsec.ErrMalformedJSON)
	})
}

func TestUncompressedTarLayer(t *testing.T) {
	t.Run("plain tar layer verifies", func(t *testing.T) {
		fixture := buildFixture()
		tarBlob := tarLayer("app/notes.txt", "uncompressed layer\n")

		fixture.layers[0] = tarBlob
		fixture.manifest.Layers[0] = netsec.Descriptor{
			MediaType: tarLayerMediaType,
			Digest:    digestOf(tarBlob),
			Size:      int64(len(tarBlob)),
		}
		fixture.config.RootFS.DiffIDs[0] = digestOf(tarBlob)
		fixture.configRaw = mustMarshal(fixture.config)
		fixture.manifest.Config.Digest = digestOf(fixture.configRaw)
		fixture.manifest.Config.Size = int64(len(fixture.configRaw))
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireNoError(t, err)
	})

	t.Run("gzip blob advertised as plain tar fails diff_id", func(t *testing.T) {
		fixture := buildFixture()
		fixture.manifest.Layers[0].MediaType = tarLayerMediaType
		fixture.rebuildManifest()
		fixture.statement = statementFor(fixture.manifestRaw)
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.Verify(fixture.manifestRaw, stmt, fixture.configRaw, fixture.layers)
		requireErrorIs(t, err, netsec.ErrDiffID)
	})
}

func TestStatementVersionsAndShapes(t *testing.T) {
	t.Run("v0.1 statement envelope accepted", func(t *testing.T) {
		fixture := buildFixture()
		fixture.statement.Type = statementV01
		fixture.rebuildStatement()

		stmt, err := netsec.ParseStatement(fixture.statementRaw)
		requireNoError(t, err)
		err = netsec.VerifyManifestSubject(fixture.manifestRaw, stmt)
		requireNoError(t, err)
	})

	t.Run("unknown statement type rejected", func(t *testing.T) {
		fixture := buildFixture()
		fixture.statement.Type = "https://evil.example/NotAStatement/v9"
		fixture.rebuildStatement()

		_, err := netsec.ParseStatement(fixture.statementRaw)
		requireErrorIs(t, err, netsec.ErrMalformedJSON)
	})

	t.Run("empty subject list rejected", func(t *testing.T) {
		fixture := buildFixture()
		fixture.statement.Subject = nil
		fixture.rebuildStatement()

		_, err := netsec.ParseStatement(fixture.statementRaw)
		requireErrorIs(t, err, netsec.ErrSubjectMismatch)
	})
}

func TestDigestHelpers(t *testing.T) {
	d := digestOf([]byte("hello"))
	if d.Algorithm() != "sha256" {
		t.Fatalf("unexpected algorithm %q", d.Algorithm())
	}
	if d.Encoded() != hashHex([]byte("hello")) {
		t.Fatalf("unexpected encoded digest %q", d.Encoded())
	}
	if _, err := netsec.ParseDigest("sha512:abcd"); !errors.Is(err, netsec.ErrBadDigest) {
		t.Fatalf("expected ErrBadDigest, got %v", err)
	}
	if _, err := netsec.ParseDigest(d.String()); err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}
}
