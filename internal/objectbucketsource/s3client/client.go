package s3client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func NewS3Client(endpoint, accessKey, secretKey string) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		Region:       "us-east-1",
		UsePathStyle: true,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	})
}

func PutBucketNotification(ctx context.Context, client *s3.Client, bucket, id, topic string, events []string) error {
	s3Events := make([]s3types.Event, 0, len(events))
	for _, e := range events {
		s3Events = append(s3Events, s3types.Event(e))
	}

	_, err := client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			TopicConfigurations: []s3types.TopicConfiguration{
				{
					Id:       aws.String(id),
					Events:   s3Events,
					TopicArn: aws.String(topic),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("put-bucket-notification for %q: %w", bucket, err)
	}
	return nil
}

func RemoveBucketNotification(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket:                    aws.String(bucket),
		NotificationConfiguration: &s3types.NotificationConfiguration{},
	})
	if err != nil {
		return fmt.Errorf("remove bucket notification for %q: %w", bucket, err)
	}
	return nil
}
