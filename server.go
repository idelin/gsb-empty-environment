package netsec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// DefaultBlockSize is the TFTP default data block size.
	DefaultBlockSize = 512
	// HardMaxBlockSize is the largest block size permitted by RFC 2348 and
	// small enough to fit in a single UDP/IP packet.
	HardMaxBlockSize = 65464
)

// Config controls a root-confined TFTP server.
type Config struct {
	// Root is the only directory whose contents may be read or written.
	Root string
	// MaxBlockSize is the caller's upper bound for negotiated block sizes.
	// Zero selects DefaultBlockSize; values above HardMaxBlockSize are capped.
	MaxBlockSize int
	// AllowOverwrite permits WRQ to replace an existing file. The zero value
	// (false) rejects overwrites with ERROR 6.
	AllowOverwrite bool
	// FileMode is used for newly created files. Zero selects 0666.
	FileMode fs.FileMode
}

// Server owns a confined root directory and creates transfer sessions.
type Server struct {
	rootPath       string
	root           *os.Root
	maxBlockSize   int
	allowOverwrite bool
	fileMode       fs.FileMode
}

// NewServer opens root and returns a server. The caller should Close the
// server when it is no longer needed.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		return nil, errors.New("netsec: empty root directory")
	}
	if cfg.MaxBlockSize < 0 {
		return nil, errors.New("netsec: negative maximum block size")
	}
	maxBlockSize := cfg.MaxBlockSize
	if maxBlockSize == 0 {
		maxBlockSize = DefaultBlockSize
	}
	if maxBlockSize > HardMaxBlockSize {
		maxBlockSize = HardMaxBlockSize
	}
	fileMode := cfg.FileMode & 0o777
	if fileMode == 0 {
		fileMode = 0o666
	}
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, err
	}
	return &Server{
		rootPath:       absRoot,
		root:           root,
		maxBlockSize:   maxBlockSize,
		allowOverwrite: cfg.AllowOverwrite,
		fileMode:       fileMode,
	}, nil
}

// Close releases the root directory handle.
func (s *Server) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	root := s.root
	s.root = nil
	return root.Close()
}

// Root returns the absolute root directory.
func (s *Server) Root() string { return s.rootPath }

// MaxBlockSize returns the effective caller-supplied block-size ceiling.
func (s *Server) MaxBlockSize() int { return s.maxBlockSize }

// AllowOverwrite reports whether WRQ may replace existing files.
func (s *Server) AllowOverwrite() bool { return s.allowOverwrite }

// Accept parses an initial RRQ/WRQ datagram from client and returns a session
// plus the first response packet.
//
// The session permanently binds to client's IP address and UDP source port
// (the transfer identifier, or TID). A request that is refused produces a TFTP
// ERROR packet, a nil session, and a *RequestError. Callers should send the
// response packet whenever it is non-nil.
func (s *Server) Accept(packet []byte, client net.Addr) (*Session, []byte, error) {
	if s == nil || s.root == nil {
		return nil, ErrorPacket(ErrorNotDefined, "server is closed"), errors.New("netsec: server is closed")
	}
	remote, err := canonicalAddrPort(client)
	if err != nil {
		return nil, ErrorPacket(ErrorIllegalOperation, "bad client address"), err
	}
	req, rerr := parseRequest(packet)
	if rerr != nil {
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	rel, perr := NormalizeFilename(req.filename)
	if perr != nil {
		rerr = &RequestError{Code: ErrorAccessViolation, Message: "access violation", Err: perr}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	if !strings.EqualFold(req.mode, "octet") {
		rerr = &RequestError{Code: ErrorIllegalOperation, Message: "only octet mode is supported"}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}

	if req.opcode == OpcodeRRQ {
		return s.startRead(rel, req, remote, client)
	}
	return s.startWrite(rel, req, remote, client)
}

// StartSession is an alias for Accept.
func (s *Server) StartSession(packet []byte, client net.Addr) (*Session, []byte, error) {
	return s.Accept(packet, client)
}

// NewSession is an alias for Accept.
func (s *Server) NewSession(packet []byte, client net.Addr) (*Session, []byte, error) {
	return s.Accept(packet, client)
}

func (s *Server) startRead(rel string, req *request, remote netip.AddrPort, client net.Addr) (*Session, []byte, error) {
	f, err := s.root.Open(filepath.FromSlash(rel))
	if err != nil {
		rerr := fileRequestError(err)
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		rerr := fileRequestError(err)
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	if info.IsDir() {
		_ = f.Close()
		rerr := &RequestError{Code: ErrorAccessViolation, Message: "access violation"}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	blockSize, accepted, rerr := negotiateOptions(req.options, s.maxBlockSize, true, info.Size())
	if rerr != nil {
		_ = f.Close()
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}

	session := &Session{
		client:    client,
		remote:    remote,
		file:      f,
		path:      rel,
		blockSize: blockSize,
		isWrite:   false,
	}
	if len(accepted) > 0 {
		session.oackPending = true
		return session, OACKPacket(accepted), nil
	}
	response, err := session.sendReadBlockLocked(1)
	if err != nil {
		_ = session.Close()
		rerr = &RequestError{Code: ErrorNotDefined, Message: "read failed", Err: err}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	return session, response, nil
}

func (s *Server) startWrite(rel string, req *request, remote netip.AddrPort, client net.Addr) (*Session, []byte, error) {
	blockSize, accepted, rerr := negotiateOptions(req.options, s.maxBlockSize, false, 0)
	if rerr != nil {
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}

	existing, statErr := s.root.Stat(filepath.FromSlash(rel))
	if statErr == nil && existing.IsDir() {
		rerr = &RequestError{Code: ErrorAccessViolation, Message: "access violation"}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}
	if statErr == nil && !s.allowOverwrite {
		rerr = &RequestError{Code: ErrorFileExists, Message: "file already exists"}
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}

	flags := os.O_WRONLY | os.O_CREATE
	if s.allowOverwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := s.root.OpenFile(filepath.FromSlash(rel), flags, s.fileMode)
	if err != nil {
		rerr = fileRequestError(err)
		return nil, ErrorPacket(rerr.Code, rerr.Message), rerr
	}

	session := &Session{
		client:    client,
		remote:    remote,
		file:      f,
		path:      rel,
		blockSize: blockSize,
		isWrite:   true,
		writeSeq:  1,
	}
	if len(accepted) > 0 {
		return session, OACKPacket(accepted), nil
	}
	return session, ACKPacket(0), nil
}

func negotiateOptions(raw []Option, maxBlockSize int, isRead bool, fileSize int64) (int, []Option, *RequestError) {
	blockSize := DefaultBlockSize
	if blockSize > maxBlockSize {
		blockSize = maxBlockSize
	}
	accepted := make([]Option, 0, len(raw))
	seen := make(map[string]bool, len(raw))

	for _, opt := range raw {
		name := strings.ToLower(opt.Name)
		if seen[name] {
			return 0, nil, &RequestError{Code: ErrorOptionNegotiation, Message: "duplicate option " + name}
		}
		seen[name] = true

		switch name {
		case "blksize":
			n, overflow, ok := parseDecimal(opt.Value)
			if !ok || (!overflow && n == 0) {
				return 0, nil, &RequestError{Code: ErrorOptionNegotiation, Message: "invalid blksize"}
			}
			if overflow || n > uint64(maxBlockSize) {
				n = uint64(maxBlockSize)
			}
			if n > HardMaxBlockSize {
				n = HardMaxBlockSize
			}
			blockSize = int(n)
			accepted = append(accepted, Option{Name: "blksize", Value: strconv.Itoa(blockSize)})
		case "tsize":
			n, overflow, ok := parseDecimal(opt.Value)
			if !ok || overflow {
				return 0, nil, &RequestError{Code: ErrorOptionNegotiation, Message: "invalid tsize"}
			}
			if isRead {
				accepted = append(accepted, Option{Name: "tsize", Value: strconv.FormatInt(fileSize, 10)})
			} else {
				accepted = append(accepted, Option{Name: "tsize", Value: strconv.FormatUint(n, 10)})
			}
		case "timeout":
			n, overflow, ok := parseDecimal(opt.Value)
			if !ok || overflow || n < 1 || n > 255 {
				return 0, nil, &RequestError{Code: ErrorOptionNegotiation, Message: "invalid timeout"}
			}
			accepted = append(accepted, Option{Name: "timeout", Value: strconv.FormatUint(n, 10)})
		default:
			// Unknown options are ignored; if none remain accepted, the
			// transfer proceeds without an OACK.
		}
	}
	return blockSize, accepted, nil
}

// parseDecimal parses an unsigned decimal option value. overflow reports a
// syntactically valid value too large for uint64.
func parseDecimal(s string) (value uint64, overflow, ok bool) {
	if s == "" {
		return 0, false, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, true, true
	}
	return n, false, true
}

func fileRequestError(err error) *RequestError {
	switch {
	case errors.Is(err, fs.ErrExist):
		return &RequestError{Code: ErrorFileExists, Message: "file already exists", Err: err}
	case errors.Is(err, fs.ErrNotExist):
		return &RequestError{Code: ErrorFileNotFound, Message: "file not found", Err: err}
	case errors.Is(err, fs.ErrPermission), errors.Is(err, io.ErrUnexpectedEOF):
		return &RequestError{Code: ErrorAccessViolation, Message: "access violation", Err: err}
	default:
		return &RequestError{Code: ErrorAccessViolation, Message: "access violation", Err: err}
	}
}

func canonicalAddrPort(addr net.Addr) (netip.AddrPort, error) {
	if addr == nil {
		return netip.AddrPort{}, errors.New("netsec: nil client address")
	}
	if u, ok := addr.(*net.UDPAddr); ok {
		if u == nil || u.IP == nil {
			return netip.AddrPort{}, fmt.Errorf("netsec: incomplete UDP address %v", addr)
		}
		if u.Port < 0 || u.Port > 65535 {
			return netip.AddrPort{}, fmt.Errorf("netsec: invalid UDP port %d", u.Port)
		}
		ip, ok := netip.AddrFromSlice(u.IP)
		if !ok {
			return netip.AddrPort{}, fmt.Errorf("netsec: invalid UDP IP %v", u.IP)
		}
		if u.Zone != "" && ip.Is6() {
			ip = ip.WithZone(u.Zone)
		}
		return netip.AddrPortFrom(ip.Unmap(), uint16(u.Port)), nil
	}
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), nil
}
