package web

import "testing"

func TestAPIKeyCreateRollsBackWhenPersistenceFails(t *testing.T) {
	store := newAPIKeyStore(t.TempDir())
	if _, _, err := store.create("test"); err == nil {
		t.Fatal("expected persistence error")
	}
	if got := len(store.Keys); got != 0 {
		t.Fatalf("retained %d in-memory keys after failed save", got)
	}
}

func TestAPIKeyRevokeRollsBackWhenPersistenceFails(t *testing.T) {
	store := newAPIKeyStore(t.TempDir() + "/api-keys.json")
	record, _, err := store.create("test")
	if err != nil {
		t.Fatal(err)
	}
	store.Path = t.TempDir()
	revoked, err := store.revoke(record.ID)
	if err == nil || revoked {
		t.Fatalf("revoke=%v err=%v, want persistence failure", revoked, err)
	}
	if store.Keys[0].Revoked {
		t.Fatal("key remained revoked after failed save")
	}
}

func TestAPIKeyDeletePhysicallyRemoves(t *testing.T) {
	store := newAPIKeyStore(t.TempDir() + "/api-keys.json")
	r1, _, err := store.create("one")
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := store.create("two")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.delete(r1.ID)
	if err != nil || !deleted {
		t.Fatalf("delete=%v err=%v", deleted, err)
	}
	for _, k := range store.Keys {
		if k.ID == r1.ID {
			t.Fatal("key still present after delete")
		}
	}
	if len(store.Keys) != 1 || store.Keys[0].ID != r2.ID {
		t.Fatalf("unexpected remaining keys: %+v", store.Keys)
	}
	if deleted, _ := store.delete("no-such-id"); deleted {
		t.Fatal("delete of unknown id should report false")
	}
}

func TestAPIKeyStoreLookupAndResolveName(t *testing.T) {
	store := newAPIKeyStore(t.TempDir() + "/api-keys.json")
	record, raw, err := store.create("prod")
	if err != nil {
		t.Fatal(err)
	}

	got, ok := store.lookup(raw)
	if !ok || got.ID != record.ID || got.Name != "prod" {
		t.Fatalf("lookup(raw)=%+v ok=%v, want record %q", got, ok, record.ID)
	}
	if _, ok := store.lookup("m365_totally_unknown"); ok {
		t.Fatal("lookup of unknown key should miss")
	}
	if _, ok := store.lookup("eyJhbGciOi.example.jwt"); ok {
		t.Fatal("lookup of JWT bearer should miss")
	}
	if _, ok := store.lookup(""); ok {
		t.Fatal("lookup of empty key should miss")
	}

	// lookup 不应触碰 LastUsedAt（valid() 已负责维护）。
	if got.LastUsedAt != nil {
		t.Fatalf("lookup mutated LastUsedAt: %v", got.LastUsedAt)
	}

	if name := store.resolveName(record.ID, ""); name != "prod" {
		t.Fatalf("resolveName(by id)=%q, want %q", name, "prod")
	}
	legacy := record.Prefix[:8] + "..."
	if name := store.resolveName("", legacy); name != "prod" {
		t.Fatalf("resolveName(legacy prefix %q)=%q, want %q", legacy, name, "prod")
	}
	if name := store.resolveName("", "m365_zzz"); name != "" {
		t.Fatalf("resolveName(short prefix)=%q, want empty", name)
	}
	if name := store.resolveName("", "eyJhbGci..."); name != "" {
		t.Fatalf("resolveName(JWT prefix)=%q, want empty", name)
	}
	if name := store.resolveName("no-such-id", ""); name != "" {
		t.Fatalf("resolveName(unknown id)=%q, want empty", name)
	}

	// revoked key 仍可归因：历史用量/会话需要继续解析其名称。
	if _, err := store.revoke(record.ID); err != nil {
		t.Fatal(err)
	}
	if name := store.resolveName(record.ID, ""); name != "prod" {
		t.Fatalf("resolveName(revoked)=%q, want %q", name, "prod")
	}
}

func TestAPIKeyDeleteRollsBackWhenPersistenceFails(t *testing.T) {
	store := newAPIKeyStore(t.TempDir() + "/api-keys.json")
	record, _, err := store.create("test")
	if err != nil {
		t.Fatal(err)
	}
	store.Path = t.TempDir()
	deleted, err := store.delete(record.ID)
	if err == nil || deleted {
		t.Fatalf("delete=%v err=%v, want persistence failure", deleted, err)
	}
	if len(store.Keys) != 1 || store.Keys[0].ID != record.ID {
		t.Fatalf("key not restored after failed delete: %+v", store.Keys)
	}
}
