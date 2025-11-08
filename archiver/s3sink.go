package archiver

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awshttp "github.com/aws/smithy-go/transport/http"
)

type S3Sink struct {
	Bucket string
	Client *s3.Client
	Up     *manager.Uploader
}

func (s *S3Sink) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var re *awshttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == 404 {
		return false, nil
	}
	return false, err
}

func (s *S3Sink) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := s.Up.Upload(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(s.Bucket),
		Key:             aws.String(key),
		Body:            body,
		ContentType:     aws.String("application/x-ndjson"),
		ContentEncoding: aws.String("gzip"),
	})
	return err
}

func (s *S3Sink) PutEmpty(ctx context.Context, key string) error {
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""), // ✅ empty body
	})
	return err
}
