package mapper

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"mariner/internal/httpapi/dto"
	"mariner/internal/model"
)

// ConnectionFromRequest maps an HTTP write DTO into a domain model while
// keeping domain construction and HTTP representation separate.
func ConnectionFromRequest(input dto.ConnectionRequest) (model.Connection, error) {
	for field, item := range map[string]struct {
		value string
		limit int
	}{
		"id": {input.ID, 128}, "name": {input.Name, 128}, "bucket": {input.Bucket, 255},
		"region": {input.Region, 128}, "endpoint": {input.Endpoint, 2048}, "prefix": {input.Prefix, 4096},
		"access key": {input.AccessKey, 512}, "secret key": {input.SecretKey, 2048},
	} {
		if utf8.RuneCountInString(item.value) > item.limit {
			return model.Connection{}, errors.New(field + " is too long")
		}
		if strings.IndexFunc(item.value, unicode.IsControl) >= 0 {
			return model.Connection{}, errors.New(field + " contains control characters")
		}
	}
	connection, err := model.NewConnection(input.Name, input.Bucket)
	if err != nil {
		return model.Connection{}, err
	}
	connection.ID = input.ID
	connection.Region = input.Region
	connection.Endpoint = input.Endpoint
	connection.AccessKey = input.AccessKey
	connection.SecretKey = input.SecretKey
	connection.Prefix = input.Prefix
	return connection, nil
}

// ConnectionToResponse is the only mapping used for connection responses.
// Keeping the projection explicit prevents backend credentials from reaching
// the browser when fields are added to the domain model later.
func ConnectionToResponse(connection model.Connection) dto.ConnectionResponse {
	return dto.ConnectionResponse{
		ID:       connection.ID,
		Name:     connection.Name,
		Bucket:   connection.Bucket,
		Region:   connection.Region,
		Endpoint: connection.Endpoint,
		Prefix:   connection.Prefix,
	}
}

func ConnectionID(id string) dto.ConnectionIDResponse {
	return dto.ConnectionIDResponse{ID: id}
}
