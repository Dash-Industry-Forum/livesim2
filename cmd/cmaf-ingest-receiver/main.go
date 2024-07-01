package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const storageRoot = "./storage"

func main() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Put("/upload/*", func(w http.ResponseWriter, r *http.Request) {
		// Extract the path and filename from URL
		// Drop the first part that should be /upload or similar.
		path := r.URL.Path
		parts := strings.Split(path, "/")
		filePath := filepath.Join(storageRoot, strings.Join(parts[2:], "/"))
		err := os.MkdirAll(filepath.Dir(filePath), 0755)
		if err != nil {
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
		defer func() {
			slog.Info("Closing body", "filename", filePath)
			r.Body.Close()
		}()
		fmt.Printf("Headers %+v\n", r.Header)
		fmt.Println("Writing to", filePath)
		// Read the content from the PUT request
		// For low-latency, this will be a stream, so one
		// need to have a loop reading
		bufSize := 65536
		buf := make([]byte, bufSize)
		ofh, err := os.Create(filePath)
		if err != nil {
			http.Error(w, "Failed to create file", http.StatusInternalServerError)
			return
		}
		defer ofh.Close()
		contentLength := 0
		if r.Header.Get("Content-Length") != "" {
			fmt.Println("Content-Length", r.Header.Get("Content-Length"))
			contentLength, err = strconv.Atoi(r.Header.Get("Content-Length"))
			if err != nil {
				http.Error(w, "Failed to parse Content-Length", http.StatusBadRequest)
				return
			}
		}
		nrRead := 0
		nrWithoutData := 0
		nrWritten := 0
		for {
			n, err := r.Body.Read(buf)
			if err != nil && err != io.EOF {
				slog.Error("Failed to read request body", "err", err)
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			eof := err == io.EOF
			if n == 0 {
				if eof {
					break
				}
				nrWithoutData++
				time.Sleep(10 * time.Millisecond)
				continue
			}

			nrWithoutData = 0
			nrRead += n
			nOut, err := ofh.Write(buf[:n])
			if err != nil {
				http.Error(w, "Failed to write file", http.StatusInternalServerError)
			}
			slog.Info("wrote bytes", "n", n, "path", path)
			nrWritten += nOut
			slog.Info("Reading", "nrRead", nrRead, "contentLength", contentLength)
			if nOut != n {
				http.Error(w, "Failed to write all bytes", http.StatusInternalServerError)
			}
			if eof {
				break
			}
		}
		_, err = w.Write([]byte("File uploaded successfully"))
		if err != nil {
			slog.Error("write file", "err", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	fmt.Println("Starting server on :8080")
	err := http.ListenAndServe(":8080", r)
	if err != nil {
		slog.Error("Failed to start server", "err", err)
	}
}
