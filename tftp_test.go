package netsec

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeFilenameRejectsNULAndEscapes(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"file.txt", true},
		{"sub/../file.txt", true},
		{"./sub/file.txt", true},
		{"", false},
		{"../secret", false},
		{"sub/../../secret", false},
		{"/etc/passwd", false},
		{`C:\windows\system32\config`, false},
		{`..\secret`, false},
		{"good\x00evil", false},
	}
	for _, tc := range cases {
		got, err := NormalizeFilename(tc.name)
		if tc.ok {
			if err != nil {
				t.Fatalf("NormalizeFilename(%q) failed: %v", tc.name, err)
			}
			if got == "" || got == "." || strings.HasPrefix(got, "../") {
				t.Fatalf("NormalizeFilename(%q) = %q, want a confined relative path", tc.name, got)
			}
		} else if err == nil {
			t.Fatalf("NormalizeFilename(%q) = %q, want error", tc.name, got)
		}
	}

	if _, err := NormalizeFilename("good\x00evil"); !errors.Is(err, ErrFilenameNUL) {
		t.Fatalf("NUL error = %v, want ErrFilenameNUL", err)
	}
}

func TestReadRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	outside := writeSiblingFile(t, root, "outside-secret.txt", []byte("secret"))
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o666); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 1024})
	client := udpAddr("192.0.2.10", 40000)
	for _, name := range []string{
		"../" + filepath.Base(outside),
		"sub/../../" + filepath.Base(outside),
		outside,
	} {
		sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, name, "octet"), client)
		if sess != nil {
			t.Fatalf("RRQ %q unexpectedly created a session", name)
		}
		if err == nil {
			t.Fatalf("RRQ %q unexpectedly succeeded", name)
		}
		mustErrorCode(t, reply, ErrorAccessViolation)
	}

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "inside.txt", "octet"), client)
	if err != nil {
		t.Fatalf("confined RRQ failed: %v", err)
	}
	block, data := mustData(t, reply)
	if block != 1 || string(data) != "inside" {
		t.Fatalf("DATA block=%d data=%q, want block 1 %q", block, data, "inside")
	}
	if got := sess.HandlePacket(ACKPacket(1), client); got != nil {
		t.Fatalf("final ACK produced %v, want nil", got)
	}
}

func TestWriteRejectsPathEscapeWithoutCreatingFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "escape-created-by-wrq.txt")
	_ = os.Remove(outside)
	t.Cleanup(func() { _ = os.Remove(outside) })

	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.11", 40001)
	sess, reply, err := srv.Accept(RequestPacket(OpcodeWRQ, "../"+filepath.Base(outside), "octet"), client)
	if sess != nil || err == nil {
		t.Fatalf("escaping WRQ sess=%v err=%v, want refusal", sess, err)
	}
	mustErrorCode(t, reply, ErrorAccessViolation)
	if _, statErr := os.Stat(outside); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("escaping WRQ created %q (stat err=%v)", outside, statErr)
	}
}

func TestReadThirdPartyPacketGetsError5AndTransferContinues(t *testing.T) {
	root := t.TempDir()
	content := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes: 512 + 488
	if err := os.WriteFile(filepath.Join(root, "payload.bin"), content, 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.20", 50000)
	outsider := udpAddr("192.0.2.20", 50001)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "payload.bin", "octet"), client)
	if err != nil {
		t.Fatal(err)
	}
	block, data := mustData(t, reply)
	if block != 1 || len(data) != 512 {
		t.Fatalf("first DATA block=%d len=%d", block, len(data))
	}

	third := sess.HandlePacket(ACKPacket(1), outsider)
	mustErrorCode(t, third, ErrorUnknownTID)
	if sess.Done() {
		t.Fatal("third-party packet terminated the transfer")
	}

	reply = sess.HandlePacket(ACKPacket(1), client)
	block, data = mustData(t, reply)
	if block != 2 || len(data) != 488 {
		t.Fatalf("second DATA block=%d len=%d, want block 2 len 488", block, len(data))
	}
	if !bytes.Equal(data, content[512:]) {
		t.Fatal("second DATA payload does not match file")
	}

	mustErrorCode(t, sess.HandlePacket(ACKPacket(2), outsider), ErrorUnknownTID)
	if sess.Done() {
		t.Fatal("third-party ACK terminated the transfer")
	}

	if reply := sess.HandlePacket(ACKPacket(2), client); reply != nil {
		t.Fatalf("final legitimate ACK produced %v", reply)
	}
	if !sess.Done() {
		t.Fatal("legitimate final ACK did not finish the transfer")
	}
}

func TestWriteThirdPartyPacketGetsError5AndTransferContinues(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.21", 50010)
	outsider := udpAddr("192.0.2.22", 50010) // different IP, same port

	sess, reply, err := srv.Accept(RequestPacket(OpcodeWRQ, "written.txt", "octet"), client)
	if err != nil {
		t.Fatal(err)
	}
	if block, ok := parseACK(reply); !ok || block != 0 {
		t.Fatalf("WRQ response = %v, want ACK 0", reply)
	}

	mustErrorCode(t, sess.HandlePacket(DataPacket(1, []byte("attacker")), outsider), ErrorUnknownTID)
	if sess.Done() {
		t.Fatal("third-party DATA terminated the write transfer")
	}
	if data, err := os.ReadFile(filepath.Join(root, "written.txt")); err != nil || len(data) != 0 {
		t.Fatalf("third-party DATA was written: data=%q err=%v", data, err)
	}

	reply = sess.HandlePacket(DataPacket(1, []byte("legit")), client)
	if block, ok := parseACK(reply); !ok || block != 1 {
		t.Fatalf("legitimate DATA response = %v, want ACK 1", reply)
	}
	if !sess.Done() {
		t.Fatal("short final DATA did not finish the write")
	}
	data, err := os.ReadFile(filepath.Join(root, "written.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legit" {
		t.Fatalf("file content = %q, want %q", data, "legit")
	}
}

func TestOversizedBlksizeIsClampedToCallerLimit(t *testing.T) {
	root := t.TempDir()
	content := bytes.Repeat([]byte("x"), 3000)
	if err := os.WriteFile(filepath.Join(root, "big.bin"), content, 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 1024})
	client := udpAddr("192.0.2.30", 60000)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "big.bin", "octet",
		Option{Name: "blksize", Value: "65535"}), client)
	if err != nil {
		t.Fatal(err)
	}
	opts := mustOACK(t, reply)
	if opts["blksize"] != "1024" {
		t.Fatalf("negotiated blksize = %q, want 1024", opts["blksize"])
	}
	if sess.BlockSize() != 1024 {
		t.Fatalf("session block size = %d, want 1024", sess.BlockSize())
	}

	reply = sess.HandlePacket(ACKPacket(0), client)
	block, data := mustData(t, reply)
	if block != 1 || len(data) != 1024 {
		t.Fatalf("DATA block=%d len=%d, want 1/1024", block, len(data))
	}
	if !bytes.Equal(data, content[:1024]) {
		t.Fatal("DATA payload mismatch")
	}
}

func TestBlksizeOverflowAndInvalidValues(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("data"), 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 2048})
	client := udpAddr("192.0.2.31", 60001)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "f.txt", "octet",
		Option{Name: "blksize", Value: "999999999999999999999999999"}), client)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustOACK(t, reply)["blksize"]; got != "2048" {
		t.Fatalf("overflowing blksize negotiated as %q, want 2048", got)
	}
	if sess.BlockSize() != 2048 {
		t.Fatalf("block size = %d, want 2048", sess.BlockSize())
	}

	for _, value := range []string{"0", "abc", "-1", ""} {
		sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "f.txt", "octet",
			Option{Name: "blksize", Value: value}), client)
		if sess != nil || err == nil {
			t.Fatalf("blksize %q accepted: sess=%v err=%v", value, sess, err)
		}
		mustErrorCode(t, reply, ErrorOptionNegotiation)
	}
}

func TestDefaultBlockSizeRespectsSmallCallerLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), bytes.Repeat([]byte("z"), 200), 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 64})
	client := udpAddr("192.0.2.32", 60002)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "f.txt", "octet"), client)
	if err != nil {
		t.Fatal(err)
	}
	if sess.BlockSize() != 64 {
		t.Fatalf("block size = %d, want caller limit 64", sess.BlockSize())
	}
	_, data := mustData(t, reply)
	if len(data) != 64 {
		t.Fatalf("DATA length = %d, want 64", len(data))
	}
}

func TestWriteDeniesOverwriteByDefault(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("original"), 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.40", 30000)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeWRQ, "existing.txt", "octet"), client)
	if sess != nil || err == nil {
		t.Fatalf("overwrite accepted: sess=%v err=%v", sess, err)
	}
	mustErrorCode(t, reply, ErrorFileExists)
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "original" {
		t.Fatalf("existing file changed: data=%q err=%v", data, readErr)
	}
}

func TestWriteAllowsOverwriteOnlyWhenConfigured(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("original"), 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root, AllowOverwrite: true})
	client := udpAddr("192.0.2.41", 30001)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeWRQ, "existing.txt", "octet"), client)
	if err != nil {
		t.Fatal(err)
	}
	if block, ok := parseACK(reply); !ok || block != 0 {
		t.Fatalf("WRQ response = %v, want ACK 0", reply)
	}
	if reply := sess.HandlePacket(DataPacket(1, []byte("replacement")), client); reply == nil {
		t.Fatal("no ACK for DATA 1")
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "replacement" {
		t.Fatalf("file content = %q, want replacement", data)
	}
}

func TestWriteOverSizeBlockIsRejectedAndSessionSurvives(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 16})
	client := udpAddr("192.0.2.42", 30002)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeWRQ, "oversize.bin", "octet"), client)
	if err != nil {
		t.Fatal(err)
	}
	if block, ok := parseACK(reply); !ok || block != 0 {
		t.Fatalf("WRQ response = %v", reply)
	}
	mustErrorCode(t, sess.HandlePacket(DataPacket(1, bytes.Repeat([]byte("a"), 17)), client), ErrorIllegalOperation)
	if sess.Done() {
		t.Fatal("oversized DATA terminated the session")
	}
	if reply := sess.HandlePacket(DataPacket(1, []byte("ok")), client); reply == nil {
		t.Fatal("session did not accept a valid block after oversized DATA")
	}
}

func TestExactMultipleUsesZeroLengthFinalBlock(t *testing.T) {
	root := t.TempDir()
	content := []byte("abcd")
	if err := os.WriteFile(filepath.Join(root, "exact.bin"), content, 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root, MaxBlockSize: 4})
	client := udpAddr("192.0.2.50", 30003)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "exact.bin", "octet",
		Option{Name: "blksize", Value: "4"}), client)
	if err != nil {
		t.Fatal(err)
	}
	mustOACK(t, reply)
	reply = sess.HandlePacket(ACKPacket(0), client)
	block, data := mustData(t, reply)
	if block != 1 || !bytes.Equal(data, content) {
		t.Fatalf("block=%d data=%q", block, data)
	}
	if sess.Done() {
		t.Fatal("full-size block must not finish before the zero-length block")
	}
	reply = sess.HandlePacket(ACKPacket(1), client)
	block, data = mustData(t, reply)
	if block != 2 || len(data) != 0 {
		t.Fatalf("final block=%d len=%d, want 2/0", block, len(data))
	}
	// Duplicate ACK retransmits the same final block without advancing.
	duplicate := sess.HandlePacket(ACKPacket(1), client)
	if !bytes.Equal(duplicate, reply) {
		t.Fatalf("duplicate ACK response = %v, want retransmission %v", duplicate, reply)
	}
	if reply := sess.HandlePacket(ACKPacket(2), client); reply != nil {
		t.Fatalf("final ACK produced %v", reply)
	}
	if !sess.Done() {
		t.Fatal("transfer not done after zero-length final block ACK")
	}
}

func TestTsizeOptionReportsFileSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sized.bin"), []byte("12345"), 0o666); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.60", 30004)

	_, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "sized.bin", "octet",
		Option{Name: "tsize", Value: "0"}), client)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustOACK(t, reply)["tsize"]; got != "5" {
		t.Fatalf("tsize = %q, want 5", got)
	}
}

func TestSymlinkEscapeIsNotReadable(t *testing.T) {
	root := t.TempDir()
	outside := writeSiblingFile(t, root, "outside-symlink-target.txt", []byte("secret"))
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	srv := newTestServer(t, Config{Root: root})
	client := udpAddr("192.0.2.70", 91000)

	sess, reply, err := srv.Accept(RequestPacket(OpcodeRRQ, "link.txt", "octet"), client)
	if sess != nil || err == nil {
		t.Fatalf("symlink escape allowed: sess=%v err=%v", sess, err)
	}
	if len(reply) < 4 || reply[0] != 0 || reply[1] != byte(OpcodeERROR) {
		t.Fatalf("expected ERROR packet, got %v", reply)
	}
}

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func udpAddr(ip string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

func writeSiblingFile(t *testing.T, root, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(root), name)
	if err := os.WriteFile(path, content, 0o666); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func mustErrorCode(t *testing.T, packet []byte, want ErrorCode) {
	t.Helper()
	if len(packet) < 5 || packet[0] != 0 || packet[1] != byte(OpcodeERROR) {
		t.Fatalf("packet = %v, want ERROR", packet)
	}
	got := ErrorCode(packet[2])<<8 | ErrorCode(packet[3])
	if got != want {
		t.Fatalf("error code = %d, want %d (packet=%v)", got, want, packet)
	}
}

func mustData(t *testing.T, packet []byte) (uint16, []byte) {
	t.Helper()
	block, data, ok := parseDATA(packet)
	if !ok {
		t.Fatalf("packet = %v, want DATA", packet)
	}
	return block, data
}

func mustOACK(t *testing.T, packet []byte) map[string]string {
	t.Helper()
	if len(packet) < 2 || packet[0] != 0 || packet[1] != byte(OpcodeOACK) {
		t.Fatalf("packet = %v, want OACK", packet)
	}
	fields, ok := zeroTerminatedFields(packet[2:])
	if !ok || len(fields)%2 != 0 {
		t.Fatalf("malformed OACK: %v", packet)
	}
	opts := make(map[string]string, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		opts[strings.ToLower(fields[i])] = fields[i+1]
	}
	return opts
}
