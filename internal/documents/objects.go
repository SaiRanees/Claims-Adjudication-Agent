package documents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// ErrNoObject: nothing is stored under the key.
var ErrNoObject = errors.New("no object under this key")

// Upload says how a client uploads one file: a PUT of the file to URL, sending Headers
// exactly as given, before ExpiresAt.
type Upload struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// Objects is the object store: one S3 bucket, SeaweedFS in development, reached with
// path-style addressing.
type Objects struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewObjects returns the store cfg describes. Calls go to cfg.Endpoint; upload URLs are
// signed for cfg.PublicEndpoint, the address a client can reach.
//
// The SDK's default request checksums stay on: they never reach a presigned PUT's URL or
// signed headers (TestPresignedPutSignsOnlyTheLengthTypeAndHost pins that), so a browser
// can use the URL as it is.
func NewObjects(cfg platform.S3Config) *Objects {
	creds := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: cfg.AccessKeyID, SecretAccessKey: cfg.SecretAccessKey, Source: "S3Config"}, nil
	})
	options := func(endpoint string) s3.Options {
		return s3.Options{
			Region:       cfg.Region,
			Credentials:  creds,
			BaseEndpoint: aws.String(endpoint),
			UsePathStyle: true,
			// Each call gets a client span, and carries the request's trace to the store.
			HTTPClient: &http.Client{Transport: otelhttp.NewTransport(awshttp.NewBuildableClient().GetTransport(),
				otelhttp.WithPropagators(platform.Propagator))},
		}
	}
	return &Objects{
		client:  s3.New(options(cfg.Endpoint)),
		presign: s3.NewPresignClient(s3.New(options(cfg.PublicEndpoint))),
		bucket:  cfg.Bucket,
	}
}

// Ping checks that the bucket exists and the credentials work.
func (o *Objects) Ping(ctx context.Context) error {
	if _, err := o.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &o.bucket}); err != nil {
		return fmt.Errorf("reaching bucket %s: %w", o.bucket, err)
	}
	return nil
}

// PresignPut returns an upload that accepts one PUT of exactly size bytes of contentType
// at key, for ttl. The signature covers the host, Content-Type and Content-Length, so the
// store refuses a body of any other length or type.
func (o *Objects) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (Upload, error) {
	req, err := o.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        &o.bucket,
		Key:           &key,
		ContentType:   &contentType,
		ContentLength: &size,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return Upload{}, fmt.Errorf("signing an upload to %s: %w", key, err)
	}
	if req.Method != http.MethodPut {
		return Upload{}, fmt.Errorf("signing an upload to %s gave a %s, not a PUT", key, req.Method)
	}
	expires, err := expiry(req.URL)
	if err != nil {
		return Upload{}, err
	}
	// The client's HTTP library sends Content-Length from the body, and Host from the URL.
	return Upload{URL: req.URL, Headers: map[string]string{"Content-Type": contentType}, ExpiresAt: expires}, nil
}

// expiry reads when a presigned URL stops working from its X-Amz-Date and X-Amz-Expires.
func expiry(presigned string) (time.Time, error) {
	u, err := url.Parse(presigned)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the signed URL: %w", err)
	}
	q := u.Query()
	signed, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the signed URL's X-Amz-Date: %w", err)
	}
	seconds, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the signed URL's X-Amz-Expires: %w", err)
	}
	return signed.Add(time.Duration(seconds) * time.Second), nil
}

// Size returns the size of the object under key, or ErrNoObject.
func (o *Objects) Size(ctx context.Context, key string) (int64, error) {
	out, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &o.bucket, Key: &key})
	if notFound(err) {
		return 0, ErrNoObject
	}
	if err != nil {
		return 0, fmt.Errorf("reading the size of %s: %w", key, err)
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("the store gave no size for %s", key)
	}
	return *out.ContentLength, nil
}

// Open returns the content of the object under key, or ErrNoObject. The caller closes it.
func (o *Objects) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &o.bucket, Key: &key})
	if notFound(err) {
		return nil, ErrNoObject
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	return out.Body, nil
}

// Put stores body, of size bytes, under key.
func (o *Objects) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if _, err := o.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &o.bucket,
		Key:           &key,
		ContentType:   &contentType,
		ContentLength: &size,
		Body:          body,
	}); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// notFound reports whether the store answered 404: HEAD's NotFound and GET's NoSuchKey both.
func notFound(err error) bool {
	var re *awshttp.ResponseError
	return errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound
}
