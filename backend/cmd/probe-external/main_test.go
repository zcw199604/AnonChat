package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
)

func TestOptions(t *testing.T) {
	o, err := parseOptions([]string{"-provider", "wanfeng", "-n", "2", "-match", "1s", "-proxy", "socks5://localhost:1080", "-send-image", "image.png"}, "/home/probe")
	if err != nil || o.provider != "wanfeng" || o.n != 2 || o.match != time.Second || o.state != "/home/probe/.anonchat-probe/state.json" || o.sendImage != "image.png" || o.proxy != "socks5://localhost:1080" {
		t.Fatalf("options=%+v err=%v", o, err)
	}
	for _, args := range [][]string{nil, {"-provider", "other"}, {"-provider", "chat2019", "-n", "0"}, {"-provider", "chat2019", "-match", "0s"}, {"-provider", "chat2019", "-match", "bad"}, {"-provider", "chat2019", "extra"}, {"-provider", "wanfeng", "-proxy", "file:///tmp/a"}} {
		if _, err := parseOptions(args, "/home/probe"); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestFrameRedaction(t *testing.T) {
	f := map[string]any{"code": 7, "msg": strings.Repeat("中", 230), "data": []any{map[string]any{"Token": "secret1", "identity_token": "secret2", "SID": "secret3", "pub": "secret4", "Set-Cookie": "secret5", "Authorization": "secret6", "text": "hello"}}}
	b, err := json.Marshal(safeFrame(f))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), strings.Repeat("中", 201)) || !strings.Contains(string(b), "hello") {
		t.Fatalf("unsafe output: %s", b)
	}
	if f["msg"] != strings.Repeat("中", 230) {
		t.Fatal("redaction mutated original")
	}
}

func TestStatePersistenceAndProtection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	s, err := openState(path)
	if err != nil {
		t.Fatal(err)
	}
	want := wanfeng.Binding{IdentityID: "probe-1", UpstreamUID: "upstream", IdentityToken: "secret-token", DeviceID: "device"}
	if err := s.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	s2, err := openState(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get(context.Background(), "probe-1")
	if err != nil || got == nil || *got != want {
		t.Fatalf("binding=%+v err=%v", got, err)
	}
	for path, mode := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left temporary files: %v", entries)
	}
}

func TestStateRejectsRepositoryAndSymlinkPaths(t *testing.T) {
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(repo, "probe-state.json"), filepath.Join(repo, "not-created", "nested", "state.json")} {
		if _, err := openState(path); err == nil {
			t.Fatalf("accepted repository path %s", path)
		}
	}
	dir := t.TempDir()
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := openState(filepath.Join(alias, "state.json")); err == nil {
		t.Fatal("accepted symlink into repository")
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.Symlink(filepath.Join(dir, "missing.json"), broken); err != nil {
		t.Fatal(err)
	}
	if _, err := openState(broken); err == nil {
		t.Fatal("accepted symlink state file")
	}
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(private, "bad.json")
	if err := os.WriteFile(bad, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openState(bad); err == nil {
		t.Fatal("silently ignored corrupt state")
	}
}

func TestStateRejectsInsecurePermissions(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "state.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openState(path); err == nil {
		t.Fatal("accepted readable token file")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := openState(path); err == nil {
		t.Fatal("accepted shared state directory")
	}
	info, err := os.Stat(parent)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("must not chmod an existing shared parent")
	}
}
