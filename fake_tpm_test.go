package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

type fakeCommand struct {
	tag        uint16
	code       CommandCode
	handles    []Handle
	parameters []byte
	auth       []AuthValues
	raw        []byte
}

type fakePolicy struct {
	digest []byte
	cpHash []byte
}

type fakeContext struct {
	handle Handle
	state  fakePolicy
}

type fakeTPM struct {
	mu sync.Mutex

	startup    uint64
	policies   map[Handle]*fakePolicy
	contexts   map[uint32]fakeContext
	nextCtx    uint32
	written    map[Handle][]byte
	commandLog []fakeCommand

	dropFinalResponse bool
}

func newFakeTPM() *fakeTPM {
	return &fakeTPM{
		startup:  1,
		policies: make(map[Handle]*fakePolicy),
		contexts: make(map[uint32]fakeContext),
		written:  make(map[Handle][]byte),
	}
}

func (f *fakeTPM) Execute(ctx context.Context, raw []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cmd, err := parseFakeCommand(raw)
	if err != nil {
		return nil, err
	}
	f.commandLog = append(f.commandLog, cmd)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch cmd.code {
	case CCPolicyCpHash:
		return f.policyCpHash(cmd)
	case CCPolicyNameHash:
		return f.policyNameHash(cmd)
	case CCPolicyOR:
		return f.policyOR(cmd)
	case CCPolicyGetDigest:
		return f.policyGetDigest(cmd)
	case CCContextSave:
		return f.contextSave(cmd)
	case CCContextLoad:
		return f.contextLoad(cmd)
	case CCFlushContext:
		return f.flushContext(cmd)
	case CCNVWrite:
		resp, err := f.nvWrite(cmd)
		if err != nil {
			return nil, err
		}
		if f.dropFinalResponse {
			f.dropFinalResponse = false
			return nil, context.DeadlineExceeded
		}
		return resp, nil
	default:
		return fakeResponse(TagNoSessions, 0x00000143, nil), nil
	}
}

func (f *fakeTPM) policy(h Handle, alg Algorithm) (*fakePolicy, error) {
	p := f.policies[h]
	if p == nil {
		size, err := alg.Size()
		if err != nil {
			return nil, err
		}
		p = &fakePolicy{digest: make([]byte, size)}
		f.policies[h] = p
	}
	return p, nil
}

func (f *fakeTPM) policyCpHash(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 {
		return nil, errors.New("PolicyCpHash handle count")
	}
	p, err := f.policy(cmd.handles[0], AlgSHA256)
	if err != nil {
		return nil, err
	}
	cpHash, next, err := readTPM2B(cmd.parameters, 0)
	if err != nil || next != len(cmd.parameters) {
		return nil, errors.New("PolicyCpHash parameter")
	}
	nextDigest, err := ExtendPolicyDigest(AlgSHA256, p.digest, CCPolicyCpHash, cpHash)
	if err != nil {
		return nil, err
	}
	p.digest = nextDigest
	p.cpHash = append([]byte(nil), cpHash...)
	return fakeResponse(TagNoSessions, 0, nil), nil
}

func (f *fakeTPM) policyNameHash(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 {
		return nil, errors.New("PolicyNameHash handle count")
	}
	p, err := f.policy(cmd.handles[0], AlgSHA256)
	if err != nil {
		return nil, err
	}
	nameHash, next, err := readTPM2B(cmd.parameters, 0)
	if err != nil || next != len(cmd.parameters) {
		return nil, errors.New("PolicyNameHash parameter")
	}
	nextDigest, err := ExtendPolicyDigest(AlgSHA256, p.digest, CCPolicyNameHash, nameHash)
	if err != nil {
		return nil, err
	}
	p.digest = nextDigest
	return fakeResponse(TagNoSessions, 0, nil), nil
}

func (f *fakeTPM) policyOR(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 {
		return nil, errors.New("PolicyOR handle count")
	}
	p, err := f.policy(cmd.handles[0], AlgSHA256)
	if err != nil {
		return nil, err
	}
	count, off, err := readU32(cmd.parameters, 0)
	if err != nil || count == 0 {
		return nil, errors.New("PolicyOR list")
	}
	selected := false
	branches := make([][]byte, 0, count)
	for i := uint32(0); i < count; i++ {
		branch, next, err := readTPM2B(cmd.parameters, off)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(branch, p.digest) {
			selected = true
		}
		branches = append(branches, append([]byte(nil), branch...))
		off = next
	}
	if off != len(cmd.parameters) || !selected {
		return fakeResponse(TagNoSessions, 0x00000184, nil), nil
	}
	nextDigest, err := NextPolicyORDigest(AlgSHA256, p.digest, branches)
	if err != nil {
		return nil, err
	}
	p.digest = nextDigest
	return fakeResponse(TagNoSessions, 0, nil), nil
}

func (f *fakeTPM) policyGetDigest(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 || len(cmd.parameters) != 0 {
		return nil, errors.New("PolicyGetDigest shape")
	}
	p, err := f.policy(cmd.handles[0], AlgSHA256)
	if err != nil {
		return nil, err
	}
	return fakeResponse(TagNoSessions, 0, appendTPM2B(nil, p.digest)), nil
}

func (f *fakeTPM) contextSave(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 || len(cmd.parameters) != 0 {
		return nil, errors.New("ContextSave shape")
	}
	h := cmd.handles[0]
	p, err := f.policy(h, AlgSHA256)
	if err != nil {
		return nil, err
	}
	f.nextCtx++
	id := f.nextCtx
	state := fakePolicy{digest: append([]byte(nil), p.digest...), cpHash: append([]byte(nil), p.cpHash...)}
	f.contexts[id] = fakeContext{handle: h, state: state}
	blob := make([]byte, 8)
	copy(blob[:4], []byte("CTX1"))
	binary.BigEndian.PutUint32(blob[4:], id)
	return fakeResponse(TagNoSessions, 0, appendTPM2B(nil, blob)), nil
}

func (f *fakeTPM) contextLoad(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 0 {
		return nil, errors.New("ContextLoad handles")
	}
	blob, next, err := readTPM2B(cmd.parameters, 0)
	if err != nil || next != len(cmd.parameters) || len(blob) != 8 || string(blob[:4]) != "CTX1" {
		return fakeResponse(TagNoSessions, 0x00000184, nil), nil
	}
	id := binary.BigEndian.Uint32(blob[4:])
	ctx, ok := f.contexts[id]
	if !ok {
		return fakeResponse(TagNoSessions, 0x00000184, nil), nil
	}
	p := &fakePolicy{
		digest: append([]byte(nil), ctx.state.digest...),
		cpHash: append([]byte(nil), ctx.state.cpHash...),
	}
	f.policies[ctx.handle] = p
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(ctx.handle))
	return fakeResponse(TagNoSessions, 0, out), nil
}

func (f *fakeTPM) flushContext(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 1 || len(cmd.parameters) != 0 {
		return nil, errors.New("FlushContext shape")
	}
	delete(f.policies, cmd.handles[0])
	return fakeResponse(TagNoSessions, 0, nil), nil
}

func (f *fakeTPM) nvWrite(cmd fakeCommand) ([]byte, error) {
	if len(cmd.handles) != 2 {
		return nil, errors.New("NV_Write handle count")
	}
	index := cmd.handles[1]
	data, off, err := readTPM2B(cmd.parameters, 0)
	if err != nil || off+2 != len(cmd.parameters) {
		return fakeResponse(TagNoSessions, 0x00000184, nil), nil
	}
	p, err := f.policy(cmd.authSessionHandle(), AlgSHA256)
	if err != nil {
		return nil, err
	}
	names := make([]Name, len(cmd.handles))
	for i, h := range cmd.handles {
		names[i] = testNameFor(h)
	}
	template := CommandTemplate{
		Code:       CCNVWrite,
		Handles:    cmd.handles,
		Names:      names,
		Parameters: cmd.parameters,
	}
	actual, err := template.CPHash(AlgSHA256)
	if err != nil {
		return nil, err
	}
	if len(p.cpHash) == 0 || !equalDigest(actual, p.cpHash) {
		return fakeResponse(TagSessions, 0x00000118, nil, make([]byte, sha256.Size)), nil
	}
	if _, exists := f.written[index]; exists {
		return fakeResponse(TagSessions, 0x0000014c, nil, make([]byte, sha256.Size)), nil
	}
	f.written[index] = append([]byte(nil), data...)
	nonce := sha256.Sum256(append([]byte("response-nonce"), actual...))
	return fakeResponse(TagSessions, 0, nil, nonce[:]), nil
}

func testNameFor(h Handle) Name {
	if h >= 0x01000000 && h < 0x02000000 {
		name, err := NameFromPublic(AlgSHA256, []byte(fmt.Sprintf("nv-index-%08x", uint32(h))))
		if err == nil {
			return name
		}
	}
	return NameFromHandle(h)
}

func (f *fakeTPM) setPolicyDigest(h Handle, digest []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := f.policy(h, AlgSHA256)
	p.digest = append([]byte(nil), digest...)
}

func (f *fakeTPM) policyDigest(h Handle) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := f.policy(h, AlgSHA256)
	return append([]byte(nil), p.digest...)
}

func (f *fakeTPM) writtenAt(h Handle) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.written[h]...)
}

func (f *fakeTPM) countCommands(code CommandCode) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, cmd := range f.commandLog {
		if cmd.code == code {
			n++
		}
	}
	return n
}

func (f *fakeTPM) requests() []fakeCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCommand, len(f.commandLog))
	copy(out, f.commandLog)
	return out
}

func (c fakeCommand) authSessionHandle() Handle {
	if len(c.auth) == 0 {
		return 0
	}
	return c.auth[0].SessionHandle
}

func parseFakeCommand(raw []byte) (fakeCommand, error) {
	if len(raw) < 10 {
		return fakeCommand{}, errors.New("short command")
	}
	cmd := fakeCommand{
		tag: binary.BigEndian.Uint16(raw[0:2]),
		raw: append([]byte(nil), raw...),
	}
	size := int(binary.BigEndian.Uint32(raw[2:6]))
	cmd.code = CommandCode(binary.BigEndian.Uint32(raw[6:10]))
	if size != len(raw) {
		return fakeCommand{}, fmt.Errorf("command size %d != %d", size, len(raw))
	}
	off := 10
	handleCount := fakeHandleCount(cmd.code)
	if off+handleCount*4 > len(raw) {
		return fakeCommand{}, errors.New("short handle area")
	}
	for i := 0; i < handleCount; i++ {
		cmd.handles = append(cmd.handles, Handle(binary.BigEndian.Uint32(raw[off:off+4])))
		off += 4
	}
	if cmd.tag == TagSessions {
		authSize, next, err := readU32(raw, off)
		if err != nil {
			return fakeCommand{}, err
		}
		off = next
		authEnd := off + int(authSize)
		if authEnd < off || authEnd > len(raw) {
			return fakeCommand{}, errors.New("bad auth size")
		}
		for off < authEnd {
			handle, next, err := readU32(raw, off)
			if err != nil {
				return fakeCommand{}, err
			}
			off = next
			nonce, next, err := readTPM2B(raw, off)
			if err != nil {
				return fakeCommand{}, err
			}
			off = next
			if off >= authEnd {
				return fakeCommand{}, errors.New("short auth attributes")
			}
			attrs := raw[off]
			off++
			hmac, next, err := readTPM2B(raw, off)
			if err != nil {
				return fakeCommand{}, err
			}
			off = next
			cmd.auth = append(cmd.auth, AuthValues{
				SessionHandle: Handle(handle),
				NonceCaller:   append([]byte(nil), nonce...),
				Attributes:    attrs,
				HMAC:          append([]byte(nil), hmac...),
			})
		}
		if off != authEnd {
			return fakeCommand{}, errors.New("bad auth area")
		}
	}
	if off > len(raw) {
		return fakeCommand{}, errors.New("short parameter area")
	}
	cmd.parameters = append([]byte(nil), raw[off:]...)
	return cmd, nil
}

func fakeHandleCount(code CommandCode) int {
	switch code {
	case CCPolicyCpHash, CCPolicyNameHash, CCPolicyOR, CCPolicyGetDigest, CCContextSave, CCFlushContext:
		return 1
	case CCContextLoad:
		return 0
	case CCNVWrite:
		return 2
	default:
		return 0
	}
}

func fakeResponse(tag uint16, code uint32, params []byte, authNonce ...[]byte) []byte {
	size := 10 + len(params)
	if len(authNonce) != 0 {
		size += 4
		for _, nonce := range authNonce {
			size += 2 + len(nonce) + 1 + 2 + 1
		}
	}
	out := make([]byte, 0, size)
	out = appendU16(out, tag)
	out = appendU32(out, uint32(size))
	out = appendU32(out, code)
	if len(authNonce) != 0 {
		out = appendU32(out, uint32(len(params)))
	}
	out = append(out, params...)
	for _, nonce := range authNonce {
		out = appendTPM2B(out, nonce)
		out = append(out, 0)
		out = appendTPM2B(out, []byte{0xa5})
	}
	return out
}
