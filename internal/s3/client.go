package s3client

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"periscope/internal/tlsconfig"
	"periscope/internal/vault"
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
		options.HTTPClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}}
		if connection.Endpoint != "" {
			options.BaseEndpoint = aws.String(connection.Endpoint)
			options.UsePathStyle = true
		}
	}), nil
}
