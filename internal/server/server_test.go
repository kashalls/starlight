package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

const testToken = "secret"

func newTestServer(t *testing.T, maxSize int64) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	ts := httptest.NewServer(New(root, "https://i.example", testToken, maxSize).Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

func upload(t *testing.T, ts *httptest.Server, token string, files map[string][]byte) (*http.Response, uploadResponse) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, data := range files {
		fw, err := mw.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/upload.php", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out uploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp, out
}

func localPath(ts *httptest.Server, publicURL string) string {
	u, _ := url.Parse(publicURL)
	return ts.URL + u.RequestURI()
}

func TestUploadServeDelete(t *testing.T) {
	ts, _ := newTestServer(t, 1<<20)
	png := []byte("\x89PNG\r\n\x1a\nfake")

	resp, out := upload(t, ts, testToken, map[string][]byte{"Shot 1.PNG": png})
	if resp.StatusCode != http.StatusOK || !out.Success || len(out.Files) != 1 {
		t.Fatalf("upload: status %d, %+v", resp.StatusCode, out)
	}
	f := out.Files[0]
	if !strings.HasPrefix(f.URL, "https://i.example/") || !strings.HasSuffix(f.URL, ".png") {
		t.Fatalf("unexpected url %q", f.URL)
	}
	if f.Size != int64(len(png)) || f.Name != "Shot 1.PNG" || len(f.Hash) != 40 {
		t.Fatalf("unexpected metadata %+v", f)
	}

	get, err := http.Get(localPath(ts, f.URL))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if get.StatusCode != http.StatusOK || !bytes.Equal(data, png) {
		t.Fatalf("serve: status %d", get.StatusCode)
	}
	if ct := get.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type %q", ct)
	}
	if cc := get.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control %q", cc)
	}

	bad, _ := http.Get(localPath(ts, strings.Replace(f.DeleteURL, "key=", "key=00", 1)))
	bad.Body.Close()
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("delete with bad key: status %d", bad.StatusCode)
	}

	del, _ := http.Get(localPath(ts, f.DeleteURL))
	del.Body.Close()
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete: status %d", del.StatusCode)
	}
	gone, _ := http.Get(localPath(ts, f.URL))
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete: status %d", gone.StatusCode)
	}
}

func TestUploadRequiresToken(t *testing.T) {
	ts, dir := newTestServer(t, 1<<20)
	for _, token := range []string{"", "wrong"} {
		resp, out := upload(t, ts, token, map[string][]byte{"a.png": []byte("x")})
		if resp.StatusCode != http.StatusUnauthorized || out.Success {
			t.Fatalf("token %q: status %d", token, resp.StatusCode)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unauthorized upload stored %d files", len(entries))
	}
}

func TestUploadTooLarge(t *testing.T) {
	ts, dir := newTestServer(t, 1024)
	resp, out := upload(t, ts, testToken, map[string][]byte{"big.bin": bytes.Repeat([]byte("a"), 4096)})
	if resp.StatusCode != http.StatusRequestEntityTooLarge || out.Success {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("oversized upload left %d files", len(entries))
	}
}

func TestUnsafeExtensionDropped(t *testing.T) {
	ts, _ := newTestServer(t, 1<<20)
	_, out := upload(t, ts, testToken, map[string][]byte{"x.../../evil": []byte("x")})
	if len(out.Files) != 1 || strings.Contains(out.Files[0].URL[len("https://i.example/"):], ".") {
		t.Fatalf("unexpected result %+v", out)
	}
}

func TestServeRejectsInvalidNames(t *testing.T) {
	ts, _ := newTestServer(t, 1<<20)
	root, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	root.Body.Close()
	if root.StatusCode != http.StatusOK {
		t.Fatalf("/: status %d", root.StatusCode)
	}
	for _, p := range []string{"/..%2f..%2fetc%2fpasswd", "/short", "/healthz.png"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status %d", p, resp.StatusCode)
		}
	}
}
