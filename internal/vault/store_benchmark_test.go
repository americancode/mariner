package vault

import (
	"encoding/json"
	"fmt"
	"testing"
)

// BenchmarkMultipartPersistence compares the normalized constant-size write
// path with the previous growing-JSON parent-row update on the same SQLite
// configuration. Run with: go test ./internal/vault -run '^$' -bench BenchmarkMultipartPersistence -benchtime=1x
func BenchmarkMultipartPersistence(b *testing.B) {
	for _, partsPerUpload := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("normalized/%d-parts", partsPerUpload), func(b *testing.B) {
			store, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				id := fmt.Sprintf("normalized-%d", iteration)
				if err := store.CreateMultipartUpload(MultipartUpload{UploadID: id, UserID: "user", ConnectionID: "connection", Bucket: "bucket", Key: "key", HashState: "hash"}); err != nil {
					b.Fatal(err)
				}
				for part := 1; part <= partsPerUpload; part++ {
					if advanced, err := store.SaveMultipartPart(id, int32(part), fmt.Sprintf("etag-%d", part), 64*1024*1024, "hash"); err != nil || !advanced {
						b.Fatalf("part %d: advanced=%v err=%v", part, advanced, err)
					}
				}
			}
		})

		b.Run(fmt.Sprintf("legacy-growing-json/%d-parts", partsPerUpload), func(b *testing.B) {
			store, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			type partRecord struct {
				ETag       string `json:"ETag"`
				PartNumber int32  `json:"PartNumber"`
			}
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				id := fmt.Sprintf("legacy-%d", iteration)
				if err := store.CreateMultipartUpload(MultipartUpload{UploadID: id, UserID: "user", ConnectionID: "connection", Bucket: "bucket", Key: "key", HashState: "hash"}); err != nil {
					b.Fatal(err)
				}
				parts := make([]partRecord, 0, partsPerUpload)
				for part := 1; part <= partsPerUpload; part++ {
					parts = append(parts, partRecord{ETag: fmt.Sprintf("etag-%d", part), PartNumber: int32(part)})
					raw, err := json.Marshal(parts)
					if err != nil {
						b.Fatal(err)
					}
					if _, err = store.writeDB.Exec(`UPDATE multipart_uploads SET next_part=next_part+1,parts_json=?,hash_state='hash' WHERE upload_id=?`, string(raw), id); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
