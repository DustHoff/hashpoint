package storage

import (
	"context"
	"testing"
)

func TestPluginSettingsRepo_EnabledDefaultsTrue(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	en, err := repo.GetEnabled(ctx, "fresh-plugin")
	if err != nil {
		t.Fatalf("GetEnabled: %v", err)
	}
	if !en {
		t.Fatalf("a plugin with no state row should default to enabled")
	}
}

func TestPluginSettingsRepo_SetEnabledRoundtrip(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	if err := repo.SetEnabled(ctx, "p1", false); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}
	en, err := repo.GetEnabled(ctx, "p1")
	if err != nil || en {
		t.Fatalf("after disable: en=%v err=%v", en, err)
	}
	if err := repo.SetEnabled(ctx, "p1", true); err != nil {
		t.Fatalf("SetEnabled true: %v", err)
	}
	en, err = repo.GetEnabled(ctx, "p1")
	if err != nil || !en {
		t.Fatalf("after re-enable: en=%v err=%v", en, err)
	}
}

func TestPluginSettingsRepo_PlainAndSecretFieldsRoundtrip(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	if err := repo.SetField(ctx, "p1", "api_url", "https://example.com"); err != nil {
		t.Fatalf("SetField: %v", err)
	}
	if err := repo.SetSecretField(ctx, "p1", "api_token", "s3cret"); err != nil {
		t.Fatalf("SetSecretField: %v", err)
	}

	plain, secrets, err := repo.GetFields(ctx, "p1")
	if err != nil {
		t.Fatalf("GetFields: %v", err)
	}
	if plain["api_url"] != "https://example.com" {
		t.Fatalf("plain api_url: got %q", plain["api_url"])
	}
	if secrets["api_token"] != "s3cret" {
		t.Fatalf("secret api_token: got %q", secrets["api_token"])
	}
	if _, ok := plain["api_token"]; ok {
		t.Fatalf("secret leaked into plain map")
	}
	if _, ok := secrets["api_url"]; ok {
		t.Fatalf("plain leaked into secrets map")
	}
}

func TestPluginSettingsRepo_UpsertOverwrites(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	if err := repo.SetField(ctx, "p1", "k", "v1"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := repo.SetField(ctx, "p1", "k", "v2"); err != nil {
		t.Fatalf("second set: %v", err)
	}
	plain, _, err := repo.GetFields(ctx, "p1")
	if err != nil {
		t.Fatalf("GetFields: %v", err)
	}
	if plain["k"] != "v2" {
		t.Fatalf("upsert did not overwrite: got %q", plain["k"])
	}
}

func TestPluginSettingsRepo_DeleteField(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	_ = repo.SetField(ctx, "p1", "k1", "v1")
	_ = repo.SetSecretField(ctx, "p1", "k2", "v2")
	if err := repo.DeleteField(ctx, "p1", "k1"); err != nil {
		t.Fatalf("DeleteField plain: %v", err)
	}
	if err := repo.DeleteField(ctx, "p1", "k2"); err != nil {
		t.Fatalf("DeleteField secret: %v", err)
	}
	if err := repo.DeleteField(ctx, "p1", "missing"); err != nil {
		t.Fatalf("DeleteField missing should be no-op: %v", err)
	}
	plain, secrets, _ := repo.GetFields(ctx, "p1")
	if len(plain) != 0 || len(secrets) != 0 {
		t.Fatalf("rows remain after delete: plain=%v secrets=%v", plain, secrets)
	}
}

func TestPluginSettingsRepo_ClearWipesStateAndFields(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewPluginSettingsRepo(db, NoopCipher{})
	_ = repo.SetField(ctx, "p1", "k1", "v1")
	_ = repo.SetSecretField(ctx, "p1", "s1", "sv1")
	_ = repo.SetEnabled(ctx, "p1", false)

	if err := repo.Clear(ctx, "p1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	plain, secrets, _ := repo.GetFields(ctx, "p1")
	if len(plain) != 0 || len(secrets) != 0 {
		t.Fatalf("fields survived Clear: plain=%v secrets=%v", plain, secrets)
	}
	en, _ := repo.GetEnabled(ctx, "p1")
	if !en {
		t.Fatalf("Clear should reset enabled to the no-row default (true), got false")
	}
}

func TestPluginSettingsRepo_DecryptErrorPropagates(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// First write with a noop cipher, then swap in a cipher that fails
	// on decrypt to simulate a corrupted secret (or a copied-from-
	// another-user DB).
	repo := NewPluginSettingsRepo(db, NoopCipher{})
	if err := repo.SetSecretField(ctx, "p1", "broken", "x"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	failing := NewPluginSettingsRepo(db, failingCipher{})
	if _, _, err := failing.GetFields(ctx, "p1"); err == nil {
		t.Fatalf("expected decrypt failure to surface, got nil")
	}
}

type failingCipher struct{}

func (failingCipher) Encrypt(_ []byte) ([]byte, error) { return nil, errFailingCipher }
func (failingCipher) Decrypt(_ []byte) ([]byte, error) { return nil, errFailingCipher }

var errFailingCipher = errStub("decrypt failed")

type errStub string

func (e errStub) Error() string { return string(e) }
