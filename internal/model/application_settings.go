package model

const DefaultArchiveMaxBytes int64 = 5 * 1024 * 1024 * 1024

type ApplicationSettings struct {
	ArchiveMaxBytes int64 `json:"archiveMaxBytes"`
}
