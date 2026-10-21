package s3client

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"mariner/internal/tlsconfig"
	"mariner/internal/vault"
)

func New(ctx context.Context, connection vault.Connection) (*s3.Client, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion(connection.Region)}
	if connection.AccessKey != "" {
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(connection.AccessKey, connection.SecretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	rootCAs, err := tlsconfig.RootCAs()
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.HTTPClient = &http.Client{Transport: &timedTransport{base: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}}}
		if connection.Endpoint != "" {
			options.BaseEndpoint = aws.String(connection.Endpoint)
			options.UsePathStyle = true
		}
	}), nil
}

type timedTransport struct {
	base http.RoundTripper
}

func (t *timedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := t.base.RoundTrip(request)
	duration := time.Since(started)
	fields := []any{
		"duration_ms", duration.Milliseconds(),
	}
	if err != nil {
		slog.Warn("S3 HTTP request failed", fields...)
		return nil, err
	}
	fields = append(fields,
		"status", response.StatusCode,
	)
	if response.StatusCode >= 400 {
		slog.Warn("S3 HTTP request returned an error", fields...)
	} else if duration >= 2*time.Second {
		slog.Info("S3 HTTP request slow", fields...)
	} else {
		slog.Debug("S3 HTTP request completed", fields...)
	}
	return response, nil
}
