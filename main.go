package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials" // ✅ Correct path
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var s3Client *s3.Client
var bucket string
var region string

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
	http.HandleFunc("/multipart/initiate", handleInitiateMultipart)
	http.HandleFunc("/multipart/presigned", handlePresignPart)
	http.HandleFunc("/multipart/complete", handleCompleteMultipart)

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

func handleInitiateMultipart(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "Missing filename", http.StatusBadRequest)
		return
	}

	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("uploads/" + filename),
	}

	resp, err := s3Client.CreateMultipartUpload(context.TODO(), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"uploadId": *resp.UploadId,
		"key":      *resp.Key,
	})
}

// Step 2: Generate pre-signed URL for part upload
func handlePresignPart(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	uploadId := r.URL.Query().Get("uploadId")
	partNumStr := r.URL.Query().Get("partNumber")
	partNumber, _ := strconv.Atoi(partNumStr)

	if filename == "" || uploadId == "" || partNumber == 0 {
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	presignClient := s3.NewPresignClient(s3Client)
	req, err := presignClient.PresignUploadPart(context.TODO(), &s3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String("uploads/" + filename),
		PartNumber: aws.Int32(int32(partNumber)),
		UploadId:   aws.String(uploadId),
	}, s3.WithPresignExpires(15*time.Minute))

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"url": req.URL,
	})
}

// Step 3: Complete multipart upload
func handleCompleteMultipart(w http.ResponseWriter, r *http.Request) {
	// Define a custom part structure for JSON unmarshaling
	var payload struct {
		Key      string `json:"key"`
		UploadId string `json:"uploadId"`
		Parts    []struct {
			ETag       string `json:"eTag"`
			PartNumber int32  `json:"partNumber"`
		} `json:"parts"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Convert the received parts to s3.CompletedPart format
	completedParts := make([]types.CompletedPart, len(payload.Parts))
	for i, part := range payload.Parts {
		completedParts[i] = types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(part.PartNumber),
		}
	}

	_, err := s3Client.CompleteMultipartUpload(context.TODO(), &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(payload.Key),
		UploadId: aws.String(payload.UploadId),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Upload completed"))
}
