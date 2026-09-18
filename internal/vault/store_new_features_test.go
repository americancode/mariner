package vault

import (
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLSessionRoundTripAndDelete(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := store.SaveSession("session-1", "user-1", "Demo", `["engineering","admin"]`, "encrypted-secret", expires); err != nil {
		t.Fatal(err)
	}
	userID, userName, groups, secret, loadedExpires, loadedLastSeen, found, err := store.LoadSession("session-1")
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	if userID != "user-1" || userName != "Demo" || groups != `["engineering","admin"]` || secret != "encrypted-secret" || !loadedExpires.Equal(expires) || loadedLastSeen.IsZero() {
		t.Fatalf("unexpected session: %q %q %q %q %s", userID, userName, groups, secret, loadedExpires)
	}
	if err := store.DeleteSession("session-1"); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, _, found, err = store.LoadSession("session-1")
	if err != nil || found {
		t.Fatalf("deleted session: found=%v err=%v", found, err)
	}
}

func TestSQLSessionTouchOnlyUpdatesStaleSession(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	expires := time.Now().UTC().Add(time.Hour)
	if err := store.SaveSession("session-1", "user-1", "Demo", `[]`, "", expires); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, initial, found, err := store.LoadSession("session-1")
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}

	recent := initial.Add(10 * time.Second)
	if err := store.TouchSessionIfStale("session-1", recent, initial); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, unchanged, _, err := store.LoadSession("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Equal(initial) {
		t.Fatalf("recent session was updated: got %s want %s", unchanged, initial)
	}

	stale := initial.Add(time.Minute)
	if err := store.TouchSessionIfStale("session-1", stale, initial.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, updated, _, err := store.LoadSession("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Equal(stale) {
		t.Fatalf("stale session was not updated: got %s want %s", updated, stale)
	}
}

func TestSQLiteConcurrentMultipartWritesDoNotLockOrLoseState(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const uploads = 24
	for i := 0; i < uploads; i++ {
		id := fmt.Sprintf("upload-%d", i)
		if err := store.CreateMultipartUpload(MultipartUpload{UploadID: id, UserID: "user-1", ConnectionID: "connection-1", Bucket: "bucket", Key: id, HashState: "hash-0"}); err != nil {
			t.Fatal(err)
		}
	}

	errs := make(chan error, uploads)
	var wg sync.WaitGroup
	for i := 0; i < uploads; i++ {
		id := fmt.Sprintf("upload-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := int32(1); part <= 4; part++ {
				parts := fmt.Sprintf(`[{"PartNumber":%d,"ETag":"etag-%d"}]`, part, part)
				updated, err := store.SaveMultipartPart(id, part, parts, fmt.Sprintf("hash-%d", part))
				if err != nil {
					errs <- err
					return
				}
				if !updated {
					errs <- fmt.Errorf("part %d was not advanced", part)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	for i := 0; i < uploads; i++ {
		upload, found, err := store.LoadMultipartUpload(fmt.Sprintf("upload-%d", i))
		if err != nil || !found || upload.NextPart != 5 {
			t.Fatalf("upload %d final state: %+v found=%v err=%v", i, upload, found, err)
		}
	}
}

func TestSQLiteConcurrentSameMultipartPartAdvancesOnce(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateMultipartUpload(MultipartUpload{UploadID: "upload-1", UserID: "user-1", ConnectionID: "connection-1", Bucket: "bucket", Key: "file", HashState: "hash-0"}); err != nil {
		t.Fatal(err)
	}

	const attempts = 16
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			updated, err := store.SaveMultipartPart("upload-1", 1, `[{"PartNumber":1,"ETag":"etag"}]`, "hash-1")
			if err != nil {
				errs <- err
				return
			}
			results <- updated
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	updatedCount := 0
	for updated := range results {
		if updated {
			updatedCount++
		}
	}
	if updatedCount != 1 {
		t.Fatalf("expected exactly one successful part advance, got %d", updatedCount)
	}
	upload, found, err := store.LoadMultipartUpload("upload-1")
	if err != nil || !found || upload.NextPart != 2 {
		t.Fatalf("unexpected final upload state: %+v found=%v err=%v", upload, found, err)
	}
}

func TestSQLiteConcurrentSessionTouchesDoNotLock(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SaveSession("session-1", "user-1", "Demo", `[]`, "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, initial, found, err := store.LoadSession("session-1")
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}

	const attempts = 32
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.TouchSessionIfStale("session-1", initial.Add(time.Minute), initial.Add(30*time.Second))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	_, _, _, _, _, updated, found, err := store.LoadSession("session-1")
	if err != nil || !found || !updated.Equal(initial.Add(time.Minute)) {
		t.Fatalf("unexpected final session timestamp: %s found=%v err=%v", updated, found, err)
	}
}

func TestSQLMultipartStateAdvancesAndClaimsOnce(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	upload := MultipartUpload{UploadID: "upload-1", UserID: "user-1", ConnectionID: "connection-1", Bucket: "bucket", Key: "folder/file.bin", HashState: "hash-0"}
	if err := store.CreateMultipartUpload(upload); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.LoadMultipartUpload(upload.UploadID)
	if err != nil || !found || loaded.NextPart != 1 || loaded.Status != "active" {
		t.Fatalf("initial upload: %+v found=%v err=%v", loaded, found, err)
	}
	advanced, err := store.SaveMultipartPart(upload.UploadID, 1, `[{"ETag":"etag-1","PartNumber":1}]`, "hash-1")
	if err != nil || !advanced {
		t.Fatalf("advance part: advanced=%v err=%v", advanced, err)
	}
	advanced, err = store.SaveMultipartPart(upload.UploadID, 1, `[]`, "stale")
	if err != nil || advanced {
		t.Fatalf("stale advance should fail: advanced=%v err=%v", advanced, err)
	}
	claimed, ok, err := store.ClaimMultipartComplete(upload.UploadID)
	if err != nil || !ok || claimed.Status != "completing" || claimed.NextPart != 2 {
		t.Fatalf("claim: %+v ok=%v err=%v", claimed, ok, err)
	}
	_, ok, err = store.ClaimMultipartComplete(upload.UploadID)
	if err != nil || ok {
		t.Fatalf("second claim should fail: ok=%v err=%v", ok, err)
	}
	if err := store.FinishMultipartUpload(upload.UploadID, false); err != nil {
		t.Fatal(err)
	}
	reset, ok, err := store.LoadMultipartUpload(upload.UploadID)
	if err != nil || !ok || reset.Status != "active" {
		t.Fatalf("reset upload: %+v ok=%v err=%v", reset, ok, err)
	}
}
