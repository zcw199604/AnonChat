//go:build integration

package media_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMediaSourcesOwnershipLimitsAndRollback(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	owner, _ := mkIdentity(t, pool, "media-owner")
	other, _ := mkIdentity(t, pool, "media-other")
	storage, err := media.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := media.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), storage, 2, media.WithUserMaxUploadMB(1))
	folder, err := svc.CreateFolder(ctx, "library")
	if err != nil {
		t.Fatal(err)
	}
	library, err := svc.SaveItem(ctx, folder.ID, "image.png", bytes.NewReader(pngBytes), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.SaveUserItem(ctx, owner.ID, "image.png", bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	external, err := svc.SaveExternalItem(ctx, "image.png", bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source, key string
		owner       pgtype.UUID
		folder      bool
		itemID      pgtype.UUID
	}{{"library", folder.ID.String() + "/", pgtype.UUID{}, true, library.ID}, {"user", "user/" + time.Now().UTC().Format("200601") + "/", owner.ID, false, user.ID}, {"external", "external/" + time.Now().UTC().Format("200601") + "/", pgtype.UUID{}, false, external.ID}} {
		item, f, e := svc.OpenItem(ctx, tc.itemID)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(f)
		f.Close()
		if e != nil || !bytes.Equal(data, pngBytes) {
			t.Fatalf("open content=%q err=%v", data, e)
		}
		if item.Source != tc.source || item.OwnerIdentityID != tc.owner || item.FolderID.Valid != tc.folder || !strings.HasPrefix(item.StorageKey, tc.key) {
			t.Fatalf("source metadata=%+v", item)
		}
	}
	if _, err := svc.GetUserMediaForSend(ctx, user.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, owner pgtype.UUID }{{user.ID, other.ID}, {external.ID, owner.ID}, {library.ID, owner.ID}} {
		if _, err := svc.GetUserMediaForSend(ctx, tc.id, tc.owner); !errors.Is(err, media.ErrItemNotFound) {
			t.Fatalf("should reject owner/source: %v", err)
		}
	}
	oversized := append(append([]byte{}, pngBytes...), make([]byte, 1<<20)...)
	if _, err = svc.SaveUserItem(ctx, owner.ID, "big.png", bytes.NewReader(oversized)); !mediaRejected(err) {
		t.Fatalf("user limit err=%v", err)
	}
	if _, err = svc.SaveExternalItem(ctx, "big.png", bytes.NewReader(oversized)); err != nil {
		t.Fatalf("external should use independent 2MB limit: %v", err)
	}
	if _, err = svc.SaveExternalItem(ctx, "huge.png", bytes.NewReader(append(oversized, make([]byte, 1<<20)...))); !mediaRejected(err) {
		t.Fatalf("external limit err=%v", err)
	}
	if _, err = svc.SaveUserItem(ctx, owner.ID, "fake.png", strings.NewReader("not media")); !mediaRejected(err) {
		t.Fatalf("mime rejection=%v", err)
	}
	before := fileCount(t, storage.Root())
	var missing pgtype.UUID
	_ = missing.Scan("e0000000-0000-4000-8000-000000000001")
	if _, err = svc.SaveUserItem(ctx, missing, "rollback.png", bytes.NewReader(pngBytes)); err == nil {
		t.Fatal("missing owner accepted")
	}
	if after := fileCount(t, storage.Root()); after != before {
		t.Fatalf("database failure leaked file: %d->%d", before, after)
	}
}
func mediaRejected(err error) bool {
	var e *httpx.APIError
	return errors.As(err, &e) && e.Code == httpx.CodeMediaRejected
}
func fileCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
