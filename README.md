# starlight

A single-user, Pomf-compatible file host for ShareX screenshots.

- `POST /upload.php` (or `/upload`) takes multipart `files[]` and an
  `Authorization: Bearer <UPLOAD_TOKEN>` header, and returns the Pomf response
  shape (`{"success":true,"files":[{"hash","name","url","size","deleteurl"}]}`).
- `GET /<id>.<ext>` serves a file. Files never change, so they are served with
  `Cache-Control: public, max-age=31536000, immutable` and a sandboxed CSP.
- `GET /delete/<id>.<ext>?key=<key>` deletes a file. The key is an HMAC of the
  file name, so no database is needed.
- `GET /healthz` returns `{"status":"ok"}`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PUBLIC_BASE_URL` | required | Base URL used in returned links, e.g. `https://starlight.example` |
| `UPLOAD_TOKEN` | required | Bearer token for uploads; also signs deletion keys |
| `DATA_DIR` | `/data` | Where files are stored |
| `MAX_UPLOAD_SIZE` | `104857600` | Maximum request size in bytes |
| `PORT` | `8080` | Listen port |

Rotating `UPLOAD_TOKEN` invalidates every existing deletion URL.

## ShareX

Save as `starlight.sxcu` and open it with ShareX:

```json
{
  "Name": "starlight",
  "DestinationType": "ImageUploader, TextUploader, FileUploader",
  "RequestMethod": "POST",
  "RequestURL": "https://starlight.example/upload.php",
  "Headers": {
    "Authorization": "Bearer <UPLOAD_TOKEN>"
  },
  "Body": "MultipartFormData",
  "FileFormName": "files[]",
  "URL": "{json:files[0].url}",
  "DeletionURL": "{json:files[0].deleteurl}",
  "ErrorMessage": "{json:description}"
}
```

## Development

```sh
go test -race ./...
PUBLIC_BASE_URL=http://localhost:8080 UPLOAD_TOKEN=dev DATA_DIR=./data go run ./cmd/starlight
```
