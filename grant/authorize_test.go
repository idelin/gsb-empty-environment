package grant

import (
	"errors"
	"testing"
)

func TestAuthorizeHappyPathAndRejects(t *testing.T) {
	s := newTestStore(t, nil)
	enc := activeToken(t, s, baseCreate())

	good := AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: enc,
	}
	if _, err := s.Authorize(good); err != nil {
		t.Fatalf("valid call rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*AuthorizeRequest)
		want error
	}{
		{"cross principal", func(r *AuthorizeRequest) { r.Caller = "mallory" }, ErrDenied},
		{"wrong operation", func(r *AuthorizeRequest) { r.Operation = OpSign }, ErrDenied},
		{"wrong key id", func(r *AuthorizeRequest) { r.Key.KeyID = "k2" }, ErrDenied},
		{"wrong key version", func(r *AuthorizeRequest) { r.Key.Version = 2 }, ErrDenied},
		{"extra context", func(r *AuthorizeRequest) { r.Context["extra"] = "1" }, ErrDenied},
		{"missing context", func(r *AuthorizeRequest) { r.Context = Context{} }, ErrDenied},
		{"changed context", func(r *AuthorizeRequest) { r.Context = Context{"env": "dev"} }, ErrDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := good
			tc.mut(&req)
			_, err := s.Authorize(req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestSubsetConstraints(t *testing.T) {
	s := newTestStore(t, nil)
	req := baseCreate()
	req.Constraints = nil
	req.Subset = Context{"env": "prod"}
	enc := activeToken(t, s, req)

	call := AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod", "trace": "abc"}, Token: enc,
	}
	if _, err := s.Authorize(call); err != nil {
		t.Fatalf("subset should permit extra keys: %v", err)
	}

	missing := call
	missing.Context = Context{"trace": "abc"}
	if _, err := s.Authorize(missing); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing subset key: want ErrDenied, got %v", err)
	}

	wrong := call
	wrong.Context = Context{"env": "dev", "trace": "abc"}
	if _, err := s.Authorize(wrong); !errors.Is(err, ErrDenied) {
		t.Fatalf("wrong subset value: want ErrDenied, got %v", err)
	}
}

func TestRetireInvalidatesTokensAndAccessControl(t *testing.T) {
	s := newTestStore(t, nil)
	enc := activeToken(t, s, baseCreate())

	if err := s.Retire("mallory", "g1"); !errors.Is(err, ErrDenied) {
		t.Fatalf("outsider retire: want ErrDenied, got %v", err)
	}
	if err := s.Retire("bob", "g1"); err != nil {
		t.Fatalf("grantee retire: %v", err)
	}
	g, err := s.Get("g1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusRetired || g.Generation != 1 {
		t.Fatalf("after retire: status=%s gen=%d", g.Status, g.Generation)
	}

	_, err = s.Authorize(AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: enc,
	})
	if !errors.Is(err, ErrDenied) && !errors.Is(err, ErrRetired) {
		t.Fatalf("old token after retire: want denial, got %v", err)
	}

	s2 := newTestStore(t, nil)
	activeToken(t, s2, baseCreate())
	if err := s2.Retire("carol", "g1"); err != nil {
		t.Fatalf("retiring principal retire: %v", err)
	}
}

func TestKeyRotationDoesNotExtendGrant(t *testing.T) {
	s := newTestStore(t, nil)
	enc := activeToken(t, s, baseCreate())

	// A rotated key is a new (KeyID/version) resource; the grant still pins v1.
	_, err := s.Authorize(AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 2},
		Context: Context{"env": "prod"}, Token: enc,
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("rotated version must not satisfy v1 grant: %v", err)
	}
}
