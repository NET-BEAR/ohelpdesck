package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTripAndRejection(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "correct horse battery staple") {
		t.Fatal("password persisted without hashing")
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("valid password was rejected")
	}
	if VerifyPassword(hash, "wrong password") || VerifyPassword("malformed", "anything") {
		t.Fatal("invalid password accepted")
	}
}

func TestPasswordPolicyAndMalformedHashes(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := HashPassword(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("oversized password accepted")
	}
	for _, encoded := range []string{"$argon2id$v=19$m=1,t=3,p=1$abc$def", "$argon2id$v=19$m=65536,t=3,p=1$bad$bad", "$argon2id$v=19$m=65536,t=3,p=2$bad$bad"} {
		if VerifyPassword(encoded, "correct horse battery staple") {
			t.Fatal("malformed hash accepted")
		}
	}
}

func TestRolePermissionsAreExplicit(t *testing.T) {
	if !Allowed(Administrator, PermissionUserManage) || Allowed(Agent, PermissionUserManage) {
		t.Fatal("user management policy is not explicit")
	}
	if Allowed(Role("administrator"), Permission("unknown")) {
		t.Fatal("unknown permission accepted")
	}
}
