package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials" // ✅ Correct path
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var s3Client *s3.Client
var bucket = "your-bucket-name"
var region = "ap-southeast-1"

func main() {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
		config.WithCredentialsProvider(
			aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
				getEnv("AWS_ACCESS_KEY_ID", ""),
				getEnv("AWS_SECRET_ACCESS_KEY", ""),
				"",
			)),
		),
	)
	if err != nil {
		log.Fatalf("Unable to load SDK config, %v", err)
	}

	s3Client = s3.NewFromConfig(cfg)
	bucket = getEnv("AWS_BUCKET_NAME", bucket)
	region = getEnv("AWS_REGION", region)

	http.HandleFunc("/generate", handleGenerate)
	log.Println("Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "Missing filename", http.StatusBadRequest)
		return
	}

	presignClient := s3.NewPresignClient(s3Client)
	req, err := presignClient.PresignPutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("uploads/" + filename),
	}, s3.WithPresignExpires(15*time.Minute))

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprint(w, req.URL)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
