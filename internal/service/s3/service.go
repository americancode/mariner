package s3service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"mariner/internal/model"
	s3client "mariner/internal/s3"
)

type Service struct {
	clientsMu            sync.Mutex
	clients              map[model.Connection]*s3.Client
	multipartPartSize    int64
	multipartConcurrency int
}

const MultipartPartSize int64 = 16 * 1024 * 1024
const multipartUploadConcurrency = 1

type Download struct {
	Body        io.ReadCloser
	ContentType string
}

func New() *Service { return NewWithUploadSettings(MultipartPartSize, multipartUploadConcurrency) }

func NewWithUploadSettings(partSize int64, concurrency int) *Service {
	if partSize <= 0 {
		partSize = MultipartPartSize
	}
	if concurrency <= 0 {
		concurrency = multipartUploadConcurrency
	}
	return &Service{clients: make(map[model.Connection]*s3.Client), multipartPartSize: partSize, multipartConcurrency: concurrency}
}

func (s *Service) partSize() int64 {
	if s.multipartPartSize > 0 {
		return s.multipartPartSize
	}
	return MultipartPartSize
}

func (s *Service) partConcurrency() int {
	if s.multipartConcurrency > 0 {
		return s.multipartConcurrency
	}
	return multipartUploadConcurrency
}

func (s *Service) TestConnection(ctx context.Context, connection model.Connection) error {
	slog.Info("S3 connection test started", "bucket", connection.Bucket, "region", connection.Region, "custom_endpoint", connection.Endpoint != "")
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(connection.Bucket)})
	return err
}

func (s *Service) Browse(ctx context.Context, connection model.Connection, prefix, kind, continuationToken string) (model.BrowseResult, error) {
	slog.Debug("S3 browse started", "bucket", connection.Bucket, "prefix", prefix, "kind", kind)
	parsedKind, err := validateKind(kind)
	if err != nil {
		return model.BrowseResult{}, err
	}
	if prefix == "" {
		prefix = connection.Prefix
	}
	client, err := s.client(ctx, connection)
	if err != nil {
		return model.BrowseResult{}, err
	}
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(connection.Bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/"),
	}
	if continuationToken != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}
	result, err := client.ListObjectsV2(ctx, input)
	if err != nil {
		return model.BrowseResult{}, err
	}
	items := make([]model.ObjectItem, 0)
	if parsedKind != model.ObjectKindFile {
		for _, p := range result.CommonPrefixes {
			key := aws.ToString(p.Prefix)
			items = append(items, model.ObjectItem{Name: strings.TrimSuffix(strings.TrimPrefix(key, prefix), "/"), Key: key, Kind: model.ObjectKindFolder})
		}
	}
	if parsedKind != model.ObjectKindFolder {
		for _, object := range result.Contents {
			key := aws.ToString(object.Key)
			if key != prefix {
				items = append(items, model.ObjectItem{Name: path.Base(key), Key: key, Kind: model.ObjectKindFile, Size: aws.ToInt64(object.Size), Modified: object.LastModified})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind > items[j].Kind || items[i].Name < items[j].Name })
	return model.BrowseResult{Connection: connection.Name, Prefix: prefix, Items: items, NextToken: aws.ToString(result.NextContinuationToken), HasMore: aws.ToBool(result.IsTruncated)}, nil
}

func (s *Service) Search(ctx context.Context, connection model.Connection, prefix, query, kind string) (model.BrowseResult, error) {
	slog.Debug("S3 search started", "bucket", connection.Bucket, "prefix", prefix, "kind", kind)
	parsedKind, err := validateKind(kind)
	if err != nil {
		return model.BrowseResult{}, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return model.BrowseResult{}, fmt.Errorf("search query is required")
	}
	if prefix == "" {
		prefix = connection.Prefix
	}
	client, err := s.client(ctx, connection)
	if err != nil {
		return model.BrowseResult{}, err
	}
	needle := strings.ToLower(query)
	input := &s3.ListObjectsV2Input{Bucket: aws.String(connection.Bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/")}
	items := make([]model.ObjectItem, 0)
	for {
		result, listErr := client.ListObjectsV2(ctx, input)
		if listErr != nil {
			return model.BrowseResult{}, listErr
		}
		if parsedKind != model.ObjectKindFile {
			for _, p := range result.CommonPrefixes {
				key := aws.ToString(p.Prefix)
				name := strings.TrimSuffix(strings.TrimPrefix(key, prefix), "/")
				if strings.Contains(strings.ToLower(name), needle) {
					items = append(items, model.ObjectItem{Name: name, Key: key, Kind: model.ObjectKindFolder})
				}
			}
		}
		if parsedKind != model.ObjectKindFolder {
			for _, object := range result.Contents {
				key := aws.ToString(object.Key)
				name := path.Base(key)
				if key != prefix && strings.Contains(strings.ToLower(name), needle) {
					items = append(items, model.ObjectItem{Name: name, Key: key, Kind: model.ObjectKindFile, Size: aws.ToInt64(object.Size), Modified: object.LastModified})
				}
			}
		}
		if !aws.ToBool(result.IsTruncated) {
			break
		}
		input.ContinuationToken = result.NextContinuationToken
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind > items[j].Kind || items[i].Name < items[j].Name })
	return model.BrowseResult{Connection: connection.Name, Prefix: prefix, Items: items}, nil
}

func (s *Service) CreateFolder(ctx context.Context, connection model.Connection, prefix, name string) (string, error) {
	slog.Info("S3 folder creation started", "bucket", connection.Bucket, "prefix", prefix)
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		return "", errors.New("folder name is required")
	}
	prefix = strings.Trim(prefix, "/")
	key := name + "/"
	if prefix != "" {
		key = prefix + "/" + key
	}
	client, err := s.client(ctx, connection)
	if err != nil {
		return "", err
	}
	if _, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), Body: strings.NewReader("")}); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Service) Versions(ctx context.Context, connection model.Connection, key string) ([]model.ObjectVersion, error) {
	slog.Debug("S3 version listing started", "bucket", connection.Bucket, "key", key)
	client, err := s.client(ctx, connection)
	if err != nil {
		return nil, err
	}
	input := &s3.ListObjectVersionsInput{Bucket: aws.String(connection.Bucket), Prefix: aws.String(key)}
	versions := make([]model.ObjectVersion, 0)
	for {
		result, listErr := client.ListObjectVersions(ctx, input)
		if listErr != nil {
			return nil, listErr
		}
		for _, version := range result.Versions {
			if aws.ToString(version.Key) != key || version.VersionId == nil || version.LastModified == nil {
				continue
			}
			versions = append(versions, model.ObjectVersion{VersionID: aws.ToString(version.VersionId), Key: key, IsLatest: aws.ToBool(version.IsLatest), LastModified: aws.ToTime(version.LastModified), Size: aws.ToInt64(version.Size), ETag: strings.Trim(aws.ToString(version.ETag), "\"")})
		}
		for _, marker := range result.DeleteMarkers {
			if aws.ToString(marker.Key) != key || marker.VersionId == nil || marker.LastModified == nil {
				continue
			}
			versions = append(versions, model.ObjectVersion{VersionID: aws.ToString(marker.VersionId), Key: key, IsLatest: aws.ToBool(marker.IsLatest), LastModified: aws.ToTime(marker.LastModified), DeleteMarker: true})
		}
		if !aws.ToBool(result.IsTruncated) {
			break
		}
		input.KeyMarker = result.NextKeyMarker
		input.VersionIdMarker = result.NextVersionIdMarker
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].LastModified.After(versions[j].LastModified) })
	return versions, nil
}

func (s *Service) DeleteVersion(ctx context.Context, connection model.Connection, key, versionID string) error {
	slog.Info("S3 version deletion started", "bucket", connection.Bucket, "key", key)
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), VersionId: aws.String(versionID)})
	return err
}

func (s *Service) OpenObject(ctx context.Context, connection model.Connection, key, versionID string) (Download, error) {
	slog.Debug("S3 object download started", "bucket", connection.Bucket, "key", key)
	client, err := s.client(ctx, connection)
	if err != nil {
		return Download{}, err
	}
	input := &s3.GetObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key)}
	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	object, err := client.GetObject(ctx, input)
	if err != nil {
		return Download{}, err
	}
	return Download{Body: object.Body, ContentType: aws.ToString(object.ContentType)}, nil
}

func (s *Service) DownloadArchive(ctx context.Context, connection model.Connection, prefix, format string, maxBytes int64, dst io.Writer) error {
	slog.Info("S3 archive download started", "bucket", connection.Bucket, "prefix", prefix, "format", format, "max_bytes", maxBytes)
	if maxBytes <= 0 {
		return errors.New("archive size limit must be positive")
	}
	if format != "zip" && format != "tgz" {
		return errors.New("format must be zip or tgz")
	}
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}

	var zw *zip.Writer
	var tw *tar.Writer
	var gz *gzip.Writer
	if format == "zip" {
		zw = zip.NewWriter(dst)
		defer zw.Close()
	} else {
		gz = gzip.NewWriter(dst)
		tw = tar.NewWriter(gz)
		defer func() {
			_ = tw.Close()
			_ = gz.Close()
		}()
	}

	input := &s3.ListObjectsV2Input{Bucket: aws.String(connection.Bucket), Prefix: aws.String(prefix)}
	var totalBytes int64
	for {
		result, listErr := client.ListObjectsV2(ctx, input)
		if listErr != nil {
			return listErr
		}
		for _, object := range result.Contents {
			key := aws.ToString(object.Key)
			relative := strings.TrimPrefix(key, prefix)
			if relative == "" {
				continue
			}
			if err := safeArchivePath(relative); err != nil {
				return err
			}
			objectSize := aws.ToInt64(object.Size)
			if totalBytes > maxBytes-objectSize {
				return fmt.Errorf("archive exceeds configured %d byte limit", maxBytes)
			}
			totalBytes += objectSize
			body, getErr := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key)})
			if getErr != nil {
				return getErr
			}
			if format == "zip" {
				entry, createErr := zw.Create(relative)
				if createErr == nil {
					_, createErr = io.Copy(entry, body.Body)
				}
				_ = body.Body.Close()
				if createErr != nil {
					return createErr
				}
				continue
			}
			header := &tar.Header{Name: relative, Mode: 0600, Size: aws.ToInt64(object.Size), ModTime: aws.ToTime(object.LastModified)}
			copyErr := tw.WriteHeader(header)
			if copyErr == nil {
				_, copyErr = io.Copy(tw, body.Body)
			}
			_ = body.Body.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		if !aws.ToBool(result.IsTruncated) {
			return nil
		}
		input.ContinuationToken = result.NextContinuationToken
	}
}

func safeArchivePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") {
		return errors.New("object key cannot be represented safely in an archive")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return errors.New("object key cannot be represented safely in an archive")
		}
	}
	return nil
}

func (s *Service) MultipartUploads(ctx context.Context, connection model.Connection, prefix string) ([]model.MultipartUpload, error) {
	client, err := s.client(ctx, connection)
	if err != nil {
		return nil, err
	}
	input := &s3.ListMultipartUploadsInput{Bucket: aws.String(connection.Bucket)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	} else if connection.Prefix != "" {
		input.Prefix = aws.String(connection.Prefix)
	}
	uploads := make([]model.MultipartUpload, 0)
	for {
		result, listErr := client.ListMultipartUploads(ctx, input)
		if listErr != nil {
			return nil, listErr
		}
		// Some S3-compatible implementations return a nil output for an
		// otherwise successful empty listing. Treat that the same as an empty
		// page instead of dereferencing it below.
		if result == nil {
			break
		}
		for _, upload := range result.Uploads {
			if upload.UploadId == nil || upload.Key == nil || upload.Initiated == nil {
				continue
			}
			uploads = append(uploads, model.MultipartUpload{UploadID: aws.ToString(upload.UploadId), Key: aws.ToString(upload.Key), Initiated: aws.ToTime(upload.Initiated)})
		}
		if !aws.ToBool(result.IsTruncated) {
			break
		}
		// Do not loop forever if a provider marks an empty page as truncated
		// without returning continuation markers.
		if result.NextKeyMarker == nil && result.NextUploadIdMarker == nil {
			break
		}
		input.KeyMarker = result.NextKeyMarker
		input.UploadIdMarker = result.NextUploadIdMarker
	}
	sort.Slice(uploads, func(i, j int) bool { return uploads[i].Initiated.Before(uploads[j].Initiated) })
	return uploads, nil
}

func (s *Service) AbortMultipart(ctx context.Context, connection model.Connection, uploadID, key string) error {
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}
	_, err = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(connection.Bucket), UploadId: aws.String(uploadID), Key: aws.String(key)})
	return err
}

func (s *Service) DeleteFile(ctx context.Context, connection model.Connection, key string) (string, error) {
	slog.Info("S3 object deletion started", "bucket", connection.Bucket, "key", key)
	client, err := s.client(ctx, connection)
	if err != nil {
		return "", err
	}
	digest := ""
	if strings.HasSuffix(key, "/") {
		versionInput := &s3.ListObjectVersionsInput{Bucket: aws.String(connection.Bucket), Prefix: aws.String(key)}
		versionObjects := make([]types.ObjectIdentifier, 0)
		for {
			versions, listErr := client.ListObjectVersions(ctx, versionInput)
			if listErr != nil {
				return "", listErr
			}
			for _, version := range versions.Versions {
				if version.Key != nil && version.VersionId != nil {
					versionObjects = append(versionObjects, types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
				}
			}
			for _, marker := range versions.DeleteMarkers {
				if marker.Key != nil && marker.VersionId != nil {
					versionObjects = append(versionObjects, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
				}
			}
			if !aws.ToBool(versions.IsTruncated) {
				break
			}
			versionInput.KeyMarker = versions.NextKeyMarker
			versionInput.VersionIdMarker = versions.NextVersionIdMarker
		}
		if err := s.deleteObjects(ctx, client, connection.Bucket, versionObjects); err != nil {
			return "", err
		}
		if len(versionObjects) == 0 {
			var token *string
			for {
				list, listErr := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(connection.Bucket), Prefix: aws.String(key), ContinuationToken: token})
				if listErr != nil {
					return "", listErr
				}
				objects := make([]types.ObjectIdentifier, 0, len(list.Contents))
				for _, object := range list.Contents {
					objects = append(objects, types.ObjectIdentifier{Key: object.Key})
				}
				if err := s.deleteObjects(ctx, client, connection.Bucket, objects); err != nil {
					return "", err
				}
				if !aws.ToBool(list.IsTruncated) {
					break
				}
				token = list.NextContinuationToken
			}
		}
		return "", nil
	}
	digest, err = s.objectDigest(ctx, client, connection.Bucket, key)
	if err != nil {
		return "", err
	}
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key)})
	return digest, err
}

func (s *Service) Upload(ctx context.Context, connection model.Connection, key string, file io.ReaderAt, size int64, contentType, checksum string) error {
	slog.Info("S3 object upload started", "bucket", connection.Bucket, "key", key, "size", size, "content_type", contentType)
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}
	if size > 100*1024*1024 {
		return s.uploadMultipart(ctx, client, connection.Bucket, key, file, size, contentType)
	}
	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), Body: io.NewSectionReader(file, 0, size), ContentLength: aws.Int64(size), ContentType: aws.String(contentType), ChecksumSHA256: aws.String(checksum)})
	return err
}

func (s *Service) InitMultipart(ctx context.Context, connection model.Connection, key, contentType string) (string, error) {
	slog.Info("S3 multipart upload initialized", "bucket", connection.Bucket, "key", key, "content_type", contentType)
	client, err := s.client(ctx, connection)
	if err != nil {
		return "", err
	}
	created, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), ContentType: aws.String(contentType)})
	if err != nil {
		return "", err
	}
	uploadID := aws.ToString(created.UploadId)
	if uploadID == "" {
		return "", errors.New("S3 returned an empty multipart upload ID")
	}
	return uploadID, nil
}

func (s *Service) UploadPart(ctx context.Context, connection model.Connection, uploadID, key string, partNumber int32, body []byte) (string, error) {
	slog.Debug("S3 multipart part upload started", "bucket", connection.Bucket, "key", key, "part_number", partNumber, "size", len(body))
	if int64(len(body)) > s.partSize() {
		return "", errors.New("upload part is too large")
	}
	client, err := s.client(ctx, connection)
	if err != nil {
		return "", err
	}
	part, err := client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID), PartNumber: aws.Int32(partNumber), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body)))})
	if err != nil {
		return "", err
	}
	return aws.ToString(part.ETag), nil
}

func (s *Service) CompleteMultipart(ctx context.Context, connection model.Connection, uploadID, key string, parts []model.UploadPart) error {
	slog.Info("S3 multipart upload completion started", "bucket", connection.Bucket, "key", key, "parts", len(parts))
	client, err := s.client(ctx, connection)
	if err != nil {
		return err
	}
	completed := make([]types.CompletedPart, 0, len(parts))
	for _, part := range parts {
		completed = append(completed, types.CompletedPart{ETag: aws.String(part.ETag), PartNumber: aws.Int32(part.PartNumber)})
	}
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: completed}})
	if err != nil {
		_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(connection.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID)})
	}
	return err
}

func (s *Service) uploadMultipart(ctx context.Context, client *s3.Client, bucket, key string, file io.ReaderAt, size int64, contentType string) error {
	created, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), ContentType: aws.String(contentType)})
	if err != nil {
		return err
	}
	uploadID := aws.ToString(created.UploadId)
	abort := true
	defer func() {
		if abort {
			_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID)})
		}
	}()
	partSizeLimit := s.partSize()
	partCount := int((size + partSizeLimit - 1) / partSizeLimit)
	parts := make([]types.CompletedPart, partCount)
	workers := s.partConcurrency()
	if partCount < workers {
		workers = partCount
	}
	partCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var workersWG sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for range workers {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			for partIndex := range jobs {
				partNumber := int32(partIndex + 1)
				offset := int64(partIndex) * partSizeLimit
				partSize := partSizeLimit
				if remaining := size - offset; remaining < partSize {
					partSize = remaining
				}
				part, uploadErr := client.UploadPart(partCtx, &s3.UploadPartInput{
					Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
					PartNumber: aws.Int32(partNumber), Body: io.NewSectionReader(file, offset, partSize),
					ContentLength: aws.Int64(partSize),
				})
				if uploadErr != nil {
					errOnce.Do(func() { firstErr = uploadErr; cancel() })
					return
				}
				parts[partIndex] = types.CompletedPart{ETag: part.ETag, PartNumber: aws.Int32(partNumber)}
			}
		}()
	}
	for partIndex := 0; partIndex < partCount; partIndex++ {
		select {
		case jobs <- partIndex:
		case <-partCtx.Done():
			break
		}
		if partCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	workersWG.Wait()
	if firstErr != nil {
		return firstErr
	}
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}})
	if err == nil {
		abort = false
	}
	return err
}

func (s *Service) deleteObjects(ctx context.Context, client *s3.Client, bucket string, objects []types.ObjectIdentifier) error {
	for start := 0; start < len(objects); start += 1000 {
		end := start + 1000
		if end > len(objects) {
			end = len(objects)
		}
		result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(bucket), Delete: &types.Delete{Objects: objects[start:end], Quiet: aws.Bool(true)}})
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			return fmt.Errorf("S3 rejected %d object deletions", len(result.Errors))
		}
	}
	return nil
}

func (s *Service) objectDigest(ctx context.Context, client *s3.Client, bucket, key string) (string, error) {
	object, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		return "", err
	}
	if object.ChecksumSHA256 == nil || aws.ToString(object.ChecksumSHA256) == "" {
		return "", nil
	}
	digest, err := base64.StdEncoding.DecodeString(aws.ToString(object.ChecksumSHA256))
	if err != nil {
		return "", nil
	}
	return hex.EncodeToString(digest), nil
}

func (s *Service) client(ctx context.Context, connection model.Connection) (*s3.Client, error) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if s.clients == nil {
		s.clients = make(map[model.Connection]*s3.Client)
	}
	if client, ok := s.clients[connection]; ok {
		return client, nil
	}
	client, err := s3client.New(ctx, connection)
	if err != nil {
		return nil, err
	}
	s.clients[connection] = client
	return client, nil
}

func validateKind(kind string) (model.ObjectKind, error) {
	if kind == "" {
		kind = string(model.ObjectKindAll)
	}
	parsed := model.ObjectKind(kind)
	if parsed != model.ObjectKindAll && parsed != model.ObjectKindFile && parsed != model.ObjectKindFolder {
		return "", fmt.Errorf("invalid browse kind %q", kind)
	}
	return parsed, nil
}
