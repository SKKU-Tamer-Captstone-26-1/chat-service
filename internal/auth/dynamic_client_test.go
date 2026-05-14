package auth

import "testing"

func TestAuthDescriptorsBuild(t *testing.T) {
	descs, err := authDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if descs.validateTokenRequest == nil || descs.validateTokenResponse == nil || descs.getMeRequest == nil || descs.getMeResponse == nil {
		t.Fatalf("expected all required auth descriptors to be present: %+v", descs)
	}
}
