package main

import "testing"

func verifyInput() *Input {
	return &Input{
		Files: []FileSpec{
			file("app", "4", true, "lib"),
			file("lib", "2", true),
			file("bad", "1", false),
		},
		Shards: []ShardSpec{
			shard("s1", 2, ed(0, 1, "lib"), ed(1, 2, "app")),
		},
	}
}

func TestVerifyAcceptsValidPlan(t *testing.T) {
	v := Verify(verifyInput(), []string{"app", "lib"})
	if !v.OK {
		t.Fatalf("problems: %v", v.Problems)
	}
	if v.Cost != "6" {
		t.Fatalf("cost = %s, want 6", v.Cost)
	}
}

func TestVerifyRejectsMissingDependency(t *testing.T) {
	if v := Verify(verifyInput(), []string{"app"}); v.OK {
		t.Fatal("accepted plan without required dependency")
	}
}

func TestVerifyRejectsCorruptedFile(t *testing.T) {
	if v := Verify(verifyInput(), []string{"app", "lib", "bad"}); v.OK {
		t.Fatal("accepted plan containing corrupted file")
	}
}

func TestVerifyRejectsBrokenChain(t *testing.T) {
	if v := Verify(verifyInput(), []string{"app"}); v.OK {
		t.Fatal("accepted plan with no 0->target path")
	}
	if v := Verify(verifyInput(), []string{"lib"}); v.OK {
		t.Fatal("accepted plan stopping before target")
	}
}

func TestVerifyRejectsUnknownAndDuplicate(t *testing.T) {
	if v := Verify(verifyInput(), []string{"app", "lib", "ghost"}); v.OK {
		t.Fatal("accepted plan with unknown file")
	}
	if v := Verify(verifyInput(), []string{"app", "lib", "lib"}); v.OK {
		t.Fatal("accepted plan with duplicate file")
	}
}
