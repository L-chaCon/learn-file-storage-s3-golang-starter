package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Not the owner of the video", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
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

	randName := make([]byte, 32)
	rand.Read(randName)
	base64.RawURLEncoding.EncodeToString(randName)
	fileName := fmt.Sprintf(
		"%s.%s",
		base64.RawURLEncoding.EncodeToString(randName),
		extencion,
	)
	filePath := filepath.Join(
		cfg.assetsRoot,
		fileName,
	)

	image, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to create file", err)
		return
	}
	defer image.Close()
	_, err = io.Copy(image, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Not able to copy file", err)
		return
	}

	thumbnailURL := fmt.Sprintf(
		"http://localhost:%s/assets/%s",
		cfg.port,
		fileName,
	)
	videoData.ThumbnailURL = &thumbnailURL
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
}

func getExtencion(mediaType string) (string, error) {
	var ext string
	switch mediaType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpeg"
	case "video/mp4":
		ext = "mp4"
	default:
		return "", fmt.Errorf("%s not supported", ext)
	}
	return ext, nil
}
