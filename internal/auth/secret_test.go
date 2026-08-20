package auth

import "testing"

func TestPasswordAndTokenRoundTrip(t *testing.T) {
	hash, err := HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "password1") {
		t.Fatal("password should match")
	}
	if CheckPassword(hash, "nope") {
		t.Fatal("wrong password should fail")
	}
	raw, h, err := RandomToken("hok_")
	if err != nil {
		t.Fatal(err)
	}
	if HashSecret(raw) != h {
		t.Fatal("token hash mismatch")
	}
}
