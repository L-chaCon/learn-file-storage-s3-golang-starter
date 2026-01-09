package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Limit the upload to 1Gb
	const uploadLimit = 1 << 30
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

	// get video ID
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Auth
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Not the owner of the video", err)
		return
	}

	// Read video
	fmt.Println("uploading video", videoID, "by user", userID)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	extencion, err := getExtencion(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "No suported file extencion", err)
		return
	}

	// Create tmp file
	tmpFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating tmp file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to copy file", err)
		return
	}
	tmpFile.Seek(0, io.SeekStart)

	// get ratio and deside bucket path
	ratio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to get ratio", err)
		return
	}
	var bucketPrefix string
	switch ratio {
	case "16:9":
		bucketPrefix = "landscape"
	case "9:16":
		bucketPrefix = "portrait"
	default:
		bucketPrefix = "other"
	}

	// process video
	processPath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to prosses video", err)
		return
	}
	processFile, err := os.Open(processPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to open prosses video", err)
		return
	}
	defer processFile.Close()
	defer os.Remove(processFile.Name())
	processFile.Seek(0, io.SeekStart)

	// Create file name
	randName := make([]byte, 32)
	rand.Read(randName)
	fileName := fmt.Sprintf(
		"%s/%s.%s",
		bucketPrefix,
		base64.RawURLEncoding.EncodeToString(randName),
		extencion,
	)

	// Upload to s3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        processFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to upload to s3", err)
		return
	}

	// Save video path
	videoURL := fmt.Sprintf(
		"https://%s.s3.%s.amazonaws.com/%s",
		cfg.s3Bucket,
		cfg.s3Region,
		fileName,
	)
	videoData.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to update video", err)
		return
	}

	type response struct {
		Video database.Video
	}
	respondWithJSON(w, http.StatusOK, response{
		Video: videoData,
	})
	fmt.Println("video uploaded: ", videoID, "by user", userID)
}

func getVideoAspectRatio(filePath string) (string, error) {
	type FFProbeOut struct {
		Stream []struct {
			With               int    `json:"width"`
			Height             int    `json:"height"`
			DisplayAspectRatio string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}

	var probeData FFProbeOut
	if err := json.Unmarshal(out.Bytes(), &probeData); err != nil {
		return "", err
	}

	return probeData.Stream[0].DisplayAspectRatio, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outPath := fmt.Sprintf("%s.processing", filePath)
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outPath)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return outPath, nil
}
