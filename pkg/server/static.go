// MIT License
//
// Copyright (c) 2024 PlexObject Solutions, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// This file enhances the server package with embedded static file serving capabilities.
// It implements a file server for the k8-highlander dashboard UI using Go's embed
// functionality to bundle static files directly into the binary.
//
// The implementation supports:
// - Serving embedded static files from the compiled binary
// - Single-page application (SPA) routing by falling back to dashboard.html
// - Proper handling of API routes vs. static content routes

package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"k8s.io/klog/v2"
)

//go:embed static
var staticFiles embed.FS

// getStaticFS returns a filesystem with the static files
func getStaticFS() (fs.FS, error) {
	// The embedded path includes the "static" directory, so we need to create a sub-filesystem
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	return staticFS, nil
}

// setupStaticFileServer sets up the static file server
func (s *Server) setupStaticFileServer(mux *http.ServeMux) {
	staticFS, err := getStaticFS()
	if err != nil {
		klog.Errorf("Failed to set up static file server: %v", err)
		return
	}

	// Create a file server handler
	fileServer := http.FileServer(http.FS(staticFS))

	// Handle requests for static files
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Serve index.html for the root path
		if path == "/" || path == "/dashboard" {
			HandleDashboard(w, r)
			return
		}

		// Remove leading slash for the filesystem lookup
		path = strings.TrimPrefix(path, "/")

		// Check if the file exists
		_, err = fs.Stat(staticFS, path)
		if err != nil {
			// If the file doesn't exist, serve dashboard.html for SPA routing
			if strings.HasPrefix(path, "api/") {
				// For API paths, let other handlers take care of it
				http.NotFound(w, r)
				return
			}
			path = "dashboard.html"
		}

		// Serve the file
		r.URL.Path = "/" + path
		fileServer.ServeHTTP(w, r)
	})
}

// getDashboardHTML returns the dashboard HTML content
func getDashboardHTML() ([]byte, error) {
	staticFS, err := getStaticFS()
	if err != nil {
		return nil, err
	}

	content, err := fs.ReadFile(staticFS, "dashboard.html")
	if err != nil {
		return nil, err
	}

	return content, nil
}

// HandleDashboard serves the dashboard HTML with embedded filesystem
func HandleDashboard(w http.ResponseWriter, r *http.Request) {
	staticFS, err := getStaticFS()
	if err != nil {
		klog.Errorf("Failed to get static filesystem: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	path := r.URL.Path

	// Serve dashboard.html for the root path
	if path == "/" {
		path = "/dashboard.html"
	}

	// Remove leading slash for the filesystem lookup
	path = strings.TrimPrefix(path, "/")

	// Check if the file exists
	_, err = fs.Stat(staticFS, path)
	if err != nil {
		// If the file doesn't exist, serve dashboard.html for SPA routing
		if strings.HasPrefix(path, "api/") {
			// For API paths, return 404
			http.NotFound(w, r)
			return
		}
		path = "dashboard.html"
	}

	// Read and serve the file
	content, err := fs.ReadFile(staticFS, path)
	if err != nil {
		klog.Errorf("Failed to read file %s: %v", path, err)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Set appropriate content type
	contentType := getContentType(path)
	w.Header().Set("Content-Type", contentType)

	w.Write(content)
}

// getContentType returns the appropriate content type for a file
func getContentType(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".html"):
		return "text/html"
	case strings.HasSuffix(filename, ".css"):
		return "text/css"
	case strings.HasSuffix(filename, ".js"):
		return "application/javascript"
	case strings.HasSuffix(filename, ".json"):
		return "application/json"
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".jpg"), strings.HasSuffix(filename, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	case strings.HasSuffix(filename, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(filename, ".ico"):
		return "image/x-icon"
	default:
		return "text/plain"
	}
}
