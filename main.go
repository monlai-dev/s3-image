package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var s3Client *s3.Client
var bucket string
var region string

func main() {
	// Load environment variables first
	region = getEnv("AWS_REGION", "")
	bucket = getEnv("AWS_BUCKET_NAME", "")

	if region == "" || bucket == "" {
		log.Fatal("AWS_REGION and AWS_BUCKET_NAME must be set")
	}

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
		log.Printf("Error generating presigned URL: %v", err)
		http.Error(w, fmt.Sprintf("Failed to generate presigned URL: %v", err), http.StatusInternalServerError)
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
	// Expect "key" parameter to match the frontend
	filename := r.URL.Query().Get("key")
	if filename == "" {
		http.Error(w, "Missing key parameter", http.StatusBadRequest)
		return
	}

	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("uploads/" + filename),
	}

	resp, err := s3Client.CreateMultipartUpload(context.TODO(), input)
	if err != nil {
		log.Printf("Error initiating multipart upload: %v", err)
		http.Error(w, fmt.Sprintf("Failed to initiate multipart upload: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"uploadId": *resp.UploadId,
		"key":      *resp.Key,
	})
}

func handlePresignPart(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	uploadId := r.URL.Query().Get("uploadId")
	partNumStr := r.URL.Query().Get("partNumber")

	if filename == "" || uploadId == "" || partNumStr == "" {
		http.Error(w, "Missing required parameters (filename, uploadId, partNumber)", http.StatusBadRequest)
		return
	}

	partNumber, err := strconv.Atoi(partNumStr)
	if err != nil {
		http.Error(w, "Invalid partNumber", http.StatusBadRequest)
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
		log.Printf("Error generating presigned part URL: %v", err)
		http.Error(w, fmt.Sprintf("Failed to generate presigned part URL: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"url": req.URL,
	})
}

func handleCompleteMultipart(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Key      string `json:"key"`
		UploadId string `json:"uploadId"`
		Parts    []struct {
			ETag       string `json:"eTag"`
			PartNumber int32  `json:"partNumber"`
		} `json:"parts"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if payload.Key == "" || payload.UploadId == "" || len(payload.Parts) == 0 {
		http.Error(w, "Missing required fields (key, uploadId, parts)", http.StatusBadRequest)
		return
	}

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
		log.Printf("Error completing multipart upload: %v", err)
		http.Error(w, fmt.Sprintf("Failed to complete multipart upload: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Upload completed"))
}
