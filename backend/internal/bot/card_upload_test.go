package bot

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReadCardUpload 覆盖上传边界、缺失文件及多个文件的拒绝。
func TestReadCardUpload(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, files int
		wantError   bool
	}{
		{"边界大小", 1024, 1, false},
		{"超过上限", 1025, 1, true},
		{"缺少文件", 0, 0, true},
		{"多个文件", 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			for i := 0; i < tc.files; i++ {
				part, err := form.CreateFormFile("file", "card.json")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(bytes.Repeat([]byte("x"), tc.size)); err != nil {
					t.Fatal(err)
				}
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/", &body)
			r.Header.Set("Content-Type", form.FormDataContentType())
			name, data, err := readCardUpload(httptest.NewRecorder(), r, 1024)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v，期望错误=%v", err, tc.wantError)
			}
			if !tc.wantError && (name != "card.json" || len(data) != tc.size) {
				t.Fatalf("读取不完整: %s %d", name, len(data))
			}
		})
	}
}
