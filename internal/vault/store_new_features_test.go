package vault

import (
	"fmt"
	"reflect"
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
				updated, err := store.SaveMultipartPart(id, part, fmt.Sprintf("etag-%d", part), 1024, fmt.Sprintf("hash-%d", part))
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
			updated, err := store.SaveMultipartPart("upload-1", 1, "etag", 1024, "hash-1")
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
	advanced, err := store.SaveMultipartPart(upload.UploadID, 1, "etag-1", 1024, "hash-1")
	if err != nil || !advanced {
		t.Fatalf("advance part: advanced=%v err=%v", advanced, err)
	}
	advanced, err = store.SaveMultipartPart(upload.UploadID, 1, "stale", 1024, "stale")
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

func TestMultipartPartsAreNormalizedAndCompletionRemovesState(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const uploadID = "normalized-upload"
	if err := store.CreateMultipartUpload(MultipartUpload{UploadID: uploadID, UserID: "user-1", ConnectionID: "connection-1", Bucket: "bucket", Key: "file", HashState: "hash-0"}); err != nil {
		t.Fatal(err)
	}
	for part := int32(1); part <= 100; part++ {
		advanced, err := store.SaveMultipartPart(uploadID, part, fmt.Sprintf("etag-%d", part), 64*1024*1024, fmt.Sprintf("hash-%d", part))
		if err != nil || !advanced {
			t.Fatalf("part %d: advanced=%v err=%v", part, advanced, err)
		}
	}
	parts, err := store.ListMultipartParts(uploadID)
	if err != nil || len(parts) != 100 || parts[99].PartNumber != 100 {
		t.Fatalf("parts: count=%d err=%v", len(parts), err)
	}
	var legacyJSON string
	if err := store.db.Get(&legacyJSON, `SELECT parts_json FROM multipart_uploads WHERE upload_id='normalized-upload'`); err != nil {
		t.Fatal(err)
	}
	if legacyJSON != "[]" {
		t.Fatalf("hot parent row grew unexpectedly: %s", legacyJSON)
	}
	if err := store.FinishMultipartUpload(uploadID, true); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadMultipartUpload(uploadID); err != nil || found {
		t.Fatalf("completed parent remains: found=%v err=%v", found, err)
	}
	parts, err = store.ListMultipartParts(uploadID)
	if err != nil || len(parts) != 0 {
		t.Fatalf("completed parts remain: count=%d err=%v", len(parts), err)
	}
}

func TestLegacyMultipartJSONMigratesToPartRows(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	legacy := `[{"ETag":"etag-1","PartNumber":1},{"ETag":"etag-2","PartNumber":2}]`
	_, err = store.writeDB.Exec(`INSERT INTO multipart_uploads(upload_id,user_id,connection_id,bucket,object_key,status,next_part,parts_json,hash_state,created_at,updated_at,connection_ciphertext) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "legacy-upload", "user-1", "connection-1", "bucket", "file", "active", 3, legacy, "hash-2", now, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.migrateMultipartParts(); err != nil {
		t.Fatal(err)
	}
	parts, err := store.ListMultipartParts("legacy-upload")
	if err != nil || len(parts) != 2 || parts[0].ETag != "etag-1" || parts[1].PartNumber != 2 {
		t.Fatalf("migrated parts: %+v err=%v", parts, err)
	}
	if err := store.migrateMultipartParts(); err != nil {
		t.Fatalf("migration must be idempotent: %v", err)
	}
}

func TestNormalizedVaultStoresOneEncryptedRowPerConnection(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := Data{
		Connections: []Connection{
			{ID: "one", Name: "One", Bucket: "bucket-one", AccessKey: "access-one", SecretKey: "secret-one"},
			{ID: "two", Name: "Two", Bucket: "bucket-two", AccessKey: "access-two", SecretKey: "secret-two"},
		},
		Settings: Settings{Theme: "dark"},
	}
	if err := store.Save("user-1", "correct-password", want); err != nil {
		t.Fatal(err)
	}
	var metadata, connections, settings, legacy int
	if err := store.db.Get(&metadata, `SELECT count(*) FROM vault_metadata WHERE user_id='user-1'`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Get(&connections, `SELECT count(*) FROM vault_connections WHERE user_id='user-1'`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Get(&settings, `SELECT count(*) FROM vault_settings WHERE user_id='user-1'`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Get(&legacy, `SELECT count(*) FROM vaults WHERE user_id='user-1'`); err != nil {
		t.Fatal(err)
	}
	if metadata != 1 || connections != 2 || settings != 1 || legacy != 0 {
		t.Fatalf("unexpected normalized rows: metadata=%d connections=%d settings=%d legacy=%d", metadata, connections, settings, legacy)
	}
	var ciphertext string
	if err := store.db.Get(&ciphertext, `SELECT ciphertext FROM vault_connections WHERE user_id='user-1' AND connection_id='one'`); err != nil {
		t.Fatal(err)
	}
	if ciphertext == "" || ciphertext == "secret-one" {
		t.Fatal("connection credentials were not encrypted")
	}
	got, found, err := store.Load("user-1", "correct-password")
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: got=%+v found=%v err=%v", got, found, err)
	}
	if _, _, err := store.Load("user-1", "wrong-password"); err == nil {
		t.Fatal("wrong password unlocked normalized vault")
	}
}

func TestLegacyVaultMigratesAfterSuccessfulUnlock(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := Data{Connections: []Connection{{ID: "legacy", Name: "Legacy", Bucket: "bucket", SecretKey: "secret"}}, Settings: Settings{Theme: "light"}}
	legacy, err := encrypt(want, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.writeDB.Exec(`INSERT INTO vaults(user_id,salt,nonce,ciphertext,updated_at) VALUES(?,?,?,?,?)`, "user-1", legacy.Salt, legacy.Nonce, legacy.Ciphertext, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load("user-1", "wrong-password"); err == nil {
		t.Fatal("wrong password unexpectedly migrated vault")
	}
	var normalized int
	if err := store.db.Get(&normalized, `SELECT count(*) FROM vault_metadata WHERE user_id='user-1'`); err != nil || normalized != 0 {
		t.Fatalf("vault migrated before authentication: count=%d err=%v", normalized, err)
	}
	got, found, err := store.Load("user-1", "correct-password")
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated round trip: got=%+v found=%v err=%v", got, found, err)
	}
	var legacyRows int
	if err := store.db.Get(&legacyRows, `SELECT count(*) FROM vaults WHERE user_id='user-1'`); err != nil || legacyRows != 0 {
		t.Fatalf("legacy envelope remains: count=%d err=%v", legacyRows, err)
	}
}

func TestVaultConnectionMutationDoesNotRewriteOtherRows(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	data := Data{Connections: []Connection{{ID: "one", Name: "One", Bucket: "one"}, {ID: "two", Name: "Two", Bucket: "two"}}}
	if err := store.Save("user-1", "correct-password", data); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := store.db.Get(&before, `SELECT ciphertext FROM vault_connections WHERE user_id='user-1' AND connection_id='two'`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConnection("user-1", "correct-password", Connection{ID: "one", Name: "Updated", Bucket: "one"}); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := store.db.Get(&after, `SELECT ciphertext FROM vault_connections WHERE user_id='user-1' AND connection_id='two'`); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("updating one connection rewrote an unrelated encrypted row")
	}
}
