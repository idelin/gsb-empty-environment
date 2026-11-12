package netsec

import (
	"errors"
	"testing"
)

func releaseBinding() Binding {
	return Binding{
		RepositoryID:      "123456789",
		RepositoryOwnerID: "424242",
		SubjectPattern:    "repo:acme/deploy:ref:refs/heads/*",
		AllowedJobRefs: []string{
			"acme/deploy/.github/workflows/release.yml@refs/heads/main",
		},
		Role: RoleRelease,
	}
}

func validClaims() Claims {
	return Claims{
		RepositoryID:      "123456789",
		RepositoryOwnerID: "424242",
		Subject:           "repo:acme/deploy:ref:refs/heads/main",
		JobWorkflowRef:    "acme/deploy/.github/workflows/release.yml@refs/heads/main",
		EventName:         "push",
	}
}

func TestMatchValidReleaseClaims(t *testing.T) {
	if err := releaseBinding().Match(validClaims()); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

// 仓库被删除重建后，攻击者注册同名仓库，repository_id 必然不同。
func TestMatchRecreatedSameNameRepo(t *testing.T) {
	c := validClaims()
	c.RepositoryID = "999999999" // 新仓库的 ID，名称仍是 acme/deploy
	c.Subject = "repo:acme/deploy:ref:refs/heads/main"
	err := releaseBinding().Match(c)
	if !errors.Is(err, ErrRepositoryMismatch) {
		t.Fatalf("expected ErrRepositoryMismatch, got %v", err)
	}
}

// 仓库改名后新 owner 接管原名，owner_id 不同也必须拒绝。
func TestMatchOwnerIDMismatch(t *testing.T) {
	c := validClaims()
	c.RepositoryOwnerID = "777"
	if err := releaseBinding().Match(c); !errors.Is(err, ErrRepositoryMismatch) {
		t.Fatalf("expected ErrRepositoryMismatch, got %v", err)
	}
}

// fork 仓库发起的 pull_request 不得换发发布角色。
func TestMatchForkPullRequestDeniedRelease(t *testing.T) {
	c := validClaims()
	c.EventName = "pull_request"
	c.Subject = "repo:acme/deploy:pull_request"
	b := releaseBinding()
	b.SubjectPattern = "repo:acme/deploy:*"
	err := b.Match(c)
	if !errors.Is(err, ErrRoleNotAllowed) {
		t.Fatalf("expected ErrRoleNotAllowed, got %v", err)
	}
}

// 非发布角色不受 pull_request 限制。
func TestMatchPullRequestAllowedForNonReleaseRole(t *testing.T) {
	c := validClaims()
	c.EventName = "pull_request"
	c.Subject = "repo:acme/deploy:pull_request"
	b := releaseBinding()
	b.SubjectPattern = "repo:acme/deploy:*"
	b.Role = "read-only"
	if err := b.Match(c); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestMatchSubjectWildcardFullSegmentOnly(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		subject string
		want    bool
	}{
		{"full segment wildcard", "repo:acme/deploy:ref:refs/heads/*", "repo:acme/deploy:ref:refs/heads/main", true},
		{"wildcard not prefix", "repo:acme/deploy:ref:refs/heads/ma*", "repo:acme/deploy:ref:refs/heads/main", false},
		{"wildcard not substring", "repo:acme/*eploy:ref:refs/heads/main", "repo:acme/deploy:ref:refs/heads/main", false},
		{"segment count differs", "repo:acme/deploy:*", "repo:acme/deploy:ref:refs/heads/main", false},
		{"wildcard does not match empty segment", "repo:acme/deploy:*", "repo:acme/deploy:", false},
		{"exact match", "repo:acme/deploy:ref:refs/heads/main", "repo:acme/deploy:ref:refs/heads/main", true},
		{"literal mismatch", "repo:acme/deploy:ref:refs/heads/main", "repo:acme/deploy:ref:refs/heads/dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchSubject(tc.pattern, tc.subject); got != tc.want {
				t.Fatalf("matchSubject(%q, %q) = %v, want %v", tc.pattern, tc.subject, got, tc.want)
			}
		})
	}
}

func TestMatchJobWorkflowRefNotInAllowlist(t *testing.T) {
	c := validClaims()
	c.JobWorkflowRef = "evil/org/.github/workflows/pwn.yml@refs/heads/main"
	if err := releaseBinding().Match(c); !errors.Is(err, ErrWorkflowRefNotAllowed) {
		t.Fatalf("expected ErrWorkflowRefNotAllowed, got %v", err)
	}
}

// 许可表为空表示该绑定不校验 job_workflow_ref。
func TestMatchEmptyAllowlistSkipsCheck(t *testing.T) {
	b := releaseBinding()
	b.AllowedJobRefs = nil
	c := validClaims()
	c.JobWorkflowRef = ""
	if err := b.Match(c); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}
