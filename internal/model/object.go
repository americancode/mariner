package model

import "time"

type ObjectKind string

const (
	ObjectKindAll    ObjectKind = "all"
	ObjectKindFile   ObjectKind = "file"
	ObjectKindFolder ObjectKind = "folder"
)

type ObjectItem struct {
	Name     string     `json:"name"`
	Key      string     `json:"key"`
	Kind     ObjectKind `json:"kind"`
	Size     int64      `json:"size"`
	Modified *time.Time `json:"modified,omitempty"`
}

type BrowseResult struct {
	Connection string
	Prefix     string
	Items      []ObjectItem
	NextToken  string
	HasMore    bool
}

type ObjectVersion struct {
	VersionID    string    `json:"versionId"`
	Key          string    `json:"key"`
	IsLatest     bool      `json:"isLatest"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	DeleteMarker bool      `json:"deleteMarker"`
}

type MultipartUpload struct {
	UploadID  string    `json:"uploadId"`
	Key       string    `json:"key"`
	Initiated time.Time `json:"initiated"`
}

type UploadPart struct {
	PartNumber int32
	ETag       string
}
