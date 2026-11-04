package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/token"
)

func TestACreatedTokenAuthenticatesAsItsName(t *testing.T) {
	in := newInstall(t)
	created, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, "kma_") || created.Token.Name != "ci" || !created.Token.CreatedAt.Equal(t0) {
		t.Errorf("CreateAdminToken = %+v", created)
	}
	actor, err := in.svc.Authenticate(context.Background(), created.Secret)
	if err != nil || actor != "ci" {
		t.Errorf("Authenticate(ci's secret) = %q, %v; want ci", actor, err)
	}
	actor, err = in.svc.Authenticate(context.Background(), in.owner)
	if err != nil || actor != "owner" {
		t.Errorf("Authenticate(owner's secret) = %q, %v; want owner", actor, err)
	}
}

func TestListShowsEveryTokenWithoutItsSecret(t *testing.T) {
	in := newInstall(t)
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	list, err := in.svc.ListAdminTokens(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 2 || list.Tokens[0].Name != "ci" || list.Tokens[1].Name != "owner" {
		t.Fatalf("ListAdminTokens = %+v, want ci and owner", list)
	}
	if list.Tokens[0].RevokedAt == nil || list.Tokens[1].RevokedAt != nil {
		t.Errorf("revoked = %v, %v; want only ci revoked", list.Tokens[0].RevokedAt, list.Tokens[1].RevokedAt)
	}
}

func TestARevokedTokenIsRefusedOnTheNextRequest(t *testing.T) {
	in := newInstall(t)
	ci, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.Authenticate(context.Background(), ci.Secret); err != nil {
		t.Fatal(err)
	}
	revoked, err := in.svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(t0) {
		t.Errorf("RevokeAdminToken = %+v, want revoked at %v", revoked, t0)
	}
	if _, err := in.svc.Authenticate(context.Background(), ci.Secret); code(err) != admin.CodeUnauthorized {
		t.Errorf("Authenticate after revoke = %v, want unauthorized", err)
	}
	if _, err := in.svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "ci"}); code(err) != admin.CodeNotFound {
		t.Errorf("revoking twice = %v, want not_found", err)
	}
}

func TestRotationKeepsTheNameAndEndsTheOldSecretAtOnce(t *testing.T) {
	in := newInstall(t)
	ci, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := in.svc.RotateAdminToken(as("owner"), admin.AdminTokenName{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Secret == ci.Secret || rotated.Token.Name != "ci" {
		t.Errorf("RotateAdminToken = %+v, want a new secret for ci", rotated)
	}
	if _, err := in.svc.Authenticate(context.Background(), ci.Secret); code(err) != admin.CodeUnauthorized {
		t.Errorf("the old secret = %v, want unauthorized", err)
	}
	if actor, err := in.svc.Authenticate(context.Background(), rotated.Secret); err != nil || actor != "ci" {
		t.Errorf("the new secret = %q, %v; want ci", actor, err)
	}
}

func TestTheLastLiveTokenCannotBeRevoked(t *testing.T) {
	in := newInstall(t)
	if _, err := in.svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "owner"}); code(err) != admin.CodeConflict {
		t.Fatalf("revoking the only token = %v, want conflict", err)
	}
	if _, err := in.svc.Authenticate(context.Background(), in.owner); err != nil {
		t.Errorf("owner no longer authenticates: %v", err)
	}
	// With a second token, owner can go.
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "owner"}); err != nil {
		t.Errorf("revoking owner beside ci = %v", err)
	}
}

func TestTokenNamesAreCheckedAndUnique(t *testing.T) {
	in := newInstall(t)
	for _, name := range []string{"", "Owner", "system:detect", "has space", "-dash", strings.Repeat("a", 64)} {
		if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: name}); code(err) != admin.CodeInvalid {
			t.Errorf("CreateAdminToken(%q) = %v, want invalid", name, err)
		}
	}
	for _, name := range []string{"ci", "laptop-2", "deploy.prod", "a_b", strings.Repeat("a", 63)} {
		if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: name}); err != nil {
			t.Errorf("CreateAdminToken(%q) = %v", name, err)
		}
	}
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"}); code(err) != admin.CodeConflict {
		t.Errorf("a second ci = %v, want conflict", err)
	}
	if _, err := in.svc.RotateAdminToken(as("owner"), admin.AdminTokenName{Name: "nope"}); code(err) != admin.CodeNotFound {
		t.Errorf("rotating a missing token = %v, want not_found", err)
	}
}

func TestAuthenticateRefusesWhatIsNotALiveAdminToken(t *testing.T) {
	in := newInstall(t)
	vk, err := token.MintVirtualKey(token.NewKeyID())
	if err != nil {
		t.Fatal(err)
	}
	unknown := token.MintAdminToken().Reveal()
	badCRC := in.owner[:len(in.owner)-1] + map[bool]string{true: "A", false: "B"}[in.owner[len(in.owner)-1] != 'A']
	for name, secret := range map[string]string{
		"empty":           "",
		"garbage":         "hello",
		"a virtual key":   vk.Reveal(),
		"an unknown one":  unknown,
		"a bad checksum":  badCRC,
		"owner with junk": in.owner + "x",
	} {
		if _, err := in.svc.Authenticate(context.Background(), secret); code(err) != admin.CodeUnauthorized {
			t.Errorf("Authenticate(%s) = %v, want unauthorized", name, err)
		}
	}
}

func TestAuthenticateRecordsWhenATokenWasLastUsed(t *testing.T) {
	in := newInstall(t)
	in.now = t0.Add(time.Hour)
	if _, err := in.svc.Authenticate(context.Background(), in.owner); err != nil {
		t.Fatal(err)
	}
	list, err := in.svc.ListAdminTokens(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Tokens[0].LastUsedAt; got == nil || !got.Equal(in.now) {
		t.Errorf("LastUsedAt = %v, want %v", got, in.now)
	}
}
