// Package server implements a single-user Pomf-compatible file host.
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const idLength = 8

var (
	idAlphabet = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	validName  = regexp.MustCompile(`^[A-Za-z0-9]{8}(\.[a-z0-9]{1,10})?$`)
	validExt   = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)
)

type Server struct {
	root          *os.Root
	baseURL       string
	token         []byte
	maxUploadSize int64
}

func New(root *os.Root, baseURL, token string, maxUploadSize int64) *Server {
	return &Server{
		root:          root,
		baseURL:       strings.TrimRight(baseURL, "/"),
		token:         []byte(token),
		maxUploadSize: maxUploadSize,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("starlight\n"))
	})
	mux.HandleFunc("POST /upload.php", s.upload)
	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("GET /delete/{name}", s.delete)
	mux.HandleFunc("GET /{name}", s.serve)
	return mux
}

type uploadedFile struct {
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	DeleteURL string `json:"deleteurl"`
}

type uploadResponse struct {
	Success     bool           `json:"success"`
	Files       []uploadedFile `json:"files,omitempty"`
	ErrorCode   int            `json:"errorcode,omitempty"`
	Description string         `json:"description,omitempty"`
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(auth), s.token) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadSize)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}

	var files []uploadedFile
	var names []string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.uploadFailed(w, names, err)
			return
		}
		if part.FormName() != "files[]" || part.FileName() == "" {
			part.Close()
			continue
		}
		f, name, err := s.store(part)
		part.Close()
		if err != nil {
			s.uploadFailed(w, names, err)
			return
		}
		files = append(files, f)
		names = append(names, name)
	}

	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no files[] in request")
		return
	}
	writeJSON(w, http.StatusOK, uploadResponse{Success: true, Files: files})
}

// uploadFailed removes files already stored from the same request so a
// failed upload never leaves orphans the client has no URL for.
func (s *Server) uploadFailed(w http.ResponseWriter, stored []string, err error) {
	for _, name := range stored {
		s.root.Remove(name)
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	slog.Error("upload failed", "err", err)
	writeError(w, http.StatusInternalServerError, "upload failed")
}

func (s *Server) store(part *multipart.Part) (uploadedFile, string, error) {
	original := part.FileName()
	ext := strings.ToLower(filepath.Ext(original))
	if !validExt.MatchString(ext) {
		ext = ""
	}

	for {
		name := randomID() + ext
		out, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return uploadedFile{}, "", err
		}

		hash := sha1.New()
		size, err := io.Copy(io.MultiWriter(out, hash), part)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			s.root.Remove(name)
			return uploadedFile{}, "", err
		}

		return uploadedFile{
			Hash:      hex.EncodeToString(hash.Sum(nil)),
			Name:      original,
			URL:       s.baseURL + "/" + name,
			Size:      size,
			DeleteURL: s.baseURL + "/delete/" + name + "?key=" + s.deleteKey(name),
		}, name, nil
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	f, err := s.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	// Uploads share the origin with nothing else, but a sandboxed CSP keeps an
	// uploaded HTML or SVG file from running script if it is opened directly.
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if !validName.MatchString(name) || !hmac.Equal([]byte(key), []byte(s.deleteKey(name))) {
		http.NotFound(w, r)
		return
	}
	if err := s.root.Remove(name); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("deleted\n"))
}

func (s *Server) deleteKey(name string) string {
	mac := hmac.New(sha256.New, s.token)
	mac.Write([]byte(name))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func randomID() string {
	b := make([]byte, idLength)
	rand.Read(b)
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

func writeError(w http.ResponseWriter, status int, description string) {
	writeJSON(w, status, uploadResponse{ErrorCode: status, Description: description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
