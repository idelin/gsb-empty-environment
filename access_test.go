package netsec_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

	"example.org/netsec/q0396"
)

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	base := buf.Bytes()
	app0 := jpegSegment(0xe0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"))
	payload := append([]byte("Exif\x00\x00II*\x00"), []byte("GPSLatitude=37.7749;GPSLongitude=-122.4194")...)
	app1 := jpegSegment(0xe1, payload)

	out := make([]byte, 0, len(base)+len(app0)+len(app1))
	out = append(out, base[:2]...)
	out = append(out, app0...)
	out = append(out, app1...)
	out = append(out, base[2:]...)
	return out
}

func jpegSegment(marker byte, payload []byte) []byte {
	n := len(payload) + 2
	segment := []byte{0xff, marker, byte(n >> 8), byte(n)}
	return append(segment, payload...)
}

func fixedSubject() netsec.Subject {
	return netsec.Subject{UserID: "user-a", DeviceID: "device-a"}
}

func TestLegitimateOwnerReadsOwnSanitizedScan(t *testing.T) {
	ctx := context.Background()
	access := netsec.NewAccess()
	raw := testJPEG(t)
	if !bytes.Contains(raw, []byte("GPSLatitude")) {
		t.Fatal("test fixture is missing metadata")
	}

	info, err := access.CreateJob(ctx, netsec.JobSpec{
		Subject:     fixedSubject(),
		Image:       raw,
		ContentType: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if info.UserID != "user-a" || info.DeviceID != "device-a" {
		t.Fatalf("job is not bound to subject: %+v", info)
	}
	if info.ExpiresAt.Before(info.CreatedAt) {
		t.Fatalf("invalid job time bounds: %+v", info)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(info.ID)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("job ID is not a 256-bit opaque token: %q", info.ID)
	}

	scan, err := access.Read(ctx, fixedSubject(), info.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Contains(raw, []byte("GPSLatitude")) {
		t.Fatal("CreateJob mutated caller-owned input")
	}
	if bytes.Contains(scan.Data, []byte("GPSLatitude")) || bytes.Contains(scan.Data, []byte("37.7749")) {
		t.Fatal("scan leaked location metadata")
	}
	if scan.ContentType != "image/jpeg" {
		t.Fatalf("content type = %q", scan.ContentType)
	}

	// Returned bytes are caller-owned copies, not aliases of stored data.
	scan.Data[0] = 0
	again, err := access.Read(ctx, fixedSubject(), info.ID)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if again.Data[0] == 0 {
		t.Fatal("Read returned an alias into the store")
	}
}

func TestCrossUserAndCrossDeviceReadsAreDenied(t *testing.T) {
	ctx := context.Background()
	access := netsec.NewAccess()
	info, err := access.CreateJob(ctx, netsec.JobSpec{
		Subject:     fixedSubject(),
		Image:       testJPEG(t),
		ContentType: "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []netsec.Subject{
		{UserID: "user-b", DeviceID: "device-a"},
		{UserID: "user-a", DeviceID: "device-b"},
		{UserID: "user-b", DeviceID: "device-b"},
	}
	for _, subject := range cases {
		scan, err := access.Read(ctx, subject, info.ID)
		if err == nil || scan != nil {
			t.Fatalf("cross-subject read succeeded for %+v", subject)
		}
		if !errors.Is(err, netsec.ErrForbidden) || !errors.Is(err, netsec.ErrNotFound) {
			t.Fatalf("unexpected denial error for %+v: %v", subject, err)
		}
	}

	if _, err := access.Read(ctx, fixedSubject(), info.ID); err != nil {
		t.Fatalf("owner was affected by failed foreign reads: %v", err)
	}
}

func TestUnknownAndMalformedJobsRevealNothing(t *testing.T) {
	ctx := context.Background()
	access := netsec.NewAccess(netsec.WithFailureLimit(0, 0))

	unknownButWellFormed := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	scan, err := access.Read(ctx, fixedSubject(), unknownButWellFormed)
	if err == nil || scan != nil {
		t.Fatal("unknown job was readable")
	}
	if !errors.Is(err, netsec.ErrNotFound) || !errors.Is(err, netsec.ErrForbidden) {
		t.Fatalf("unknown job error leaks existence: %v", err)
	}

	for _, id := range []string{"", "1", "2", "../../../etc/passwd", unknownButWellFormed + "x"} {
		if _, err := access.Read(ctx, fixedSubject(), id); !errors.Is(err, netsec.ErrInvalidJobID) {
			t.Fatalf("job %q error = %v, want invalid job ID", id, err)
		}
	}
}

func TestEnumerationIsThrottledAndReturnsNoScans(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	access := netsec.NewAccess(
		netsec.WithClock(func() time.Time { return now }),
		netsec.WithFailureLimit(5, time.Minute),
	)
	info, err := access.CreateJob(ctx, netsec.JobSpec{
		Subject:     fixedSubject(),
		Image:       testJPEG(t),
		ContentType: "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}

	sawRateLimit := false
	for i := 0; i < 50; i++ {
		guess := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i)}, 32))
		scan, err := access.Read(ctx, fixedSubject(), guess)
		if scan != nil || err == nil {
			t.Fatalf("enumeration guess %d returned a scan", i)
		}
		if errors.Is(err, netsec.ErrRateLimited) {
			sawRateLimit = true
		}
	}
	if !sawRateLimit {
		t.Fatal("enumeration attempts were never rate limited")
	}

	// The limiter is deliberately conservative: even a real job ID is denied
	// while the subject is in its enumeration cool-down.
	if scan, err := access.Read(ctx, fixedSubject(), info.ID); scan != nil || !errors.Is(err, netsec.ErrRateLimited) {
		t.Fatalf("owner read during cool-down = (%v, %v), want rate limit", scan, err)
	}
}

func TestExpiredJobsAreRejected(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	access := netsec.NewAccess(netsec.WithClock(func() time.Time { return now }))

	if _, err := access.CreateJob(ctx, netsec.JobSpec{
		Subject:   fixedSubject(),
		Image:     testJPEG(t),
		ExpiresAt: now.Add(-time.Second),
	}); !errors.Is(err, netsec.ErrInvalidExpiry) {
		t.Fatalf("past expiry creation error = %v", err)
	}

	info, err := access.CreateJob(ctx, netsec.JobSpec{
		Subject:   fixedSubject(),
		Image:     testJPEG(t),
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := access.Read(ctx, fixedSubject(), info.ID); err != nil {
		t.Fatalf("unexpired read: %v", err)
	}

	now = now.Add(2 * time.Minute)
	scan, err := access.Read(ctx, fixedSubject(), info.ID)
	if err == nil || scan != nil || !errors.Is(err, netsec.ErrExpired) {
		t.Fatalf("expired read = (%v, %v), want ErrExpired", scan, err)
	}
	if n, err := access.PurgeExpired(ctx); err != nil || n != 1 {
		t.Fatalf("PurgeExpired = (%d, %v)", n, err)
	}
}

func TestJobIDsAreUnpredictableAndUnique(t *testing.T) {
	ctx := context.Background()
	access := netsec.NewAccess()
	seen := make(map[string]struct{}, 200)
	for i := 0; i < 200; i++ {
		info, err := access.CreateJob(ctx, netsec.JobSpec{Subject: fixedSubject(), Image: testJPEG(t)})
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[info.ID]; exists {
			t.Fatalf("duplicate job ID %q", info.ID)
		}
		seen[info.ID] = struct{}{}
	}
}

func TestRevokeRequiresOwnership(t *testing.T) {
	ctx := context.Background()
	access := netsec.NewAccess()
	info, err := access.CreateJob(ctx, netsec.JobSpec{Subject: fixedSubject(), Image: testJPEG(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := access.Revoke(ctx, netsec.Subject{UserID: "user-b", DeviceID: "device-a"}, info.ID); !errors.Is(err, netsec.ErrForbidden) {
		t.Fatalf("foreign revoke error = %v", err)
	}
	if _, err := access.Read(ctx, fixedSubject(), info.ID); err != nil {
		t.Fatalf("foreign revoke removed the scan: %v", err)
	}
	if err := access.Revoke(ctx, fixedSubject(), info.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if _, err := access.Read(ctx, fixedSubject(), info.ID); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("revoked read error = %v", err)
	}
}
