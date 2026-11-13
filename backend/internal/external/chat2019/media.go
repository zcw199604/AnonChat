package chat2019

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const origin = "http://v1.chat2019.cn"
const referer = origin + "/randomdeskrynew4m1phj.html?v=4m1phj"

var imagePorts = []string{"7001", "7004", "9006", "9005", "9003", "9002", "9001", "8006", "8005", "8003", "8002", "8001"}

type Media struct {
	cfg      Config
	maxBytes int64
	mu       sync.Mutex
	host     string
	expires  time.Time
	lastPort string
}

// NewMedia uses the same guarded HTTP client as Dial. maxBytes is MEDIA_MAX_UPLOAD_MB in bytes.
func NewMedia(cfg Config, maxBytes int64) *Media { return &Media{cfg: cfg, maxBytes: maxBytes} }

func (m *Media) imageHost(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host != "" && time.Now().Before(m.expires) {
		return m.host, nil
	}
	if m.cfg.HTTPClient == nil || m.maxBytes <= 0 {
		return "", errors.New("chat2019: guarded HTTP client and positive media limit required")
	}
	var result struct {
		State string `json:"state"`
		Msg   struct {
			Server string `json:"server"`
		} `json:"msg"`
	}
	if err := getJSON(ctx, m.cfg.HTTPClient, m.cfg.ImgServerURL, &result); err != nil {
		return "", err
	}
	host := strings.TrimSpace(result.Msg.Server)
	u, err := url.Parse("http://" + host)
	if err != nil || result.State != "OK" || u.Hostname() == "" || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("chat2019: invalid image host")
	}
	if host != m.host {
		m.lastPort = ""
	}
	m.host, m.expires = host, time.Now().Add(10*time.Minute)
	return host, nil
}

func (m *Media) Upload(ctx context.Context, self Self, filename string, data []byte) (string, error) {
	if len(data) == 0 || int64(len(data)) > m.maxBytes {
		return "", errors.New("chat2019: media exceeds size limit or is empty")
	}
	host, err := m.imageHost(ctx)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload_file", filename)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(data); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	u := url.URL{Scheme: "http", Host: host, Path: "/asmx/upload.asmx/ProcessRequest", RawQuery: url.Values{"act": {"uploadImgRandom"}, "userid": {self.UID}}.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return "", err
	}
	mediaHeaders(req)
	// Six lower-case letters/digits, independent of the caller's text or media data.
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 6)
	if _, err = rand.Read(suffix); err != nil {
		return "", err
	}
	for i := range suffix {
		suffix[i] = alphabet[int(suffix[i])%len(alphabet)]
	}
	req.Header.Set("Cookie", fmt.Sprintf("%s_%s_%d_%s", self.UID, self.Nickname, time.Now().Unix(), suffix))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := m.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat2019: upload HTTP status %d", resp.StatusCode)
	}
	var result struct{ State, Msg string }
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return "", err
	}
	if result.State != "OK" {
		return "", errors.New("chat2019: upload rejected")
	}
	if err = checkMediaPath(result.Msg); err != nil {
		return "", err
	}
	return result.Msg, nil
}

func (m *Media) Download(ctx context.Context, path string) ([]byte, string, error) {
	if err := checkMediaPath(path); err != nil {
		return nil, "", err
	}
	host, err := m.imageHost(ctx)
	if err != nil {
		return nil, "", err
	}
	u, _ := url.Parse("http://" + host)
	m.mu.Lock()
	last := m.lastPort
	m.mu.Unlock()
	ports := append([]string(nil), imagePorts...)
	if last != "" {
		ports = append([]string{last}, ports...)
	}
	seen := make(map[string]bool, len(ports))
	var lastErr error
	for _, port := range ports {
		if seen[port] {
			continue
		}
		seen[port] = true
		endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(u.Hostname(), port), Path: "/img/Upload/" + path}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, "", err
		}
		mediaHeaders(req)
		resp, err := m.cfg.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("chat2019: download HTTP status %d", resp.StatusCode)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, m.maxBytes+1))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if int64(len(data)) > m.maxBytes {
			return nil, "", errors.New("chat2019: media exceeds size limit")
		}
		if len(data) == 0 {
			lastErr = errors.New("chat2019: empty media")
			continue
		}
		m.mu.Lock()
		if m.host == host {
			m.lastPort = port
		}
		m.mu.Unlock()
		return data, resp.Header.Get("Content-Type"), nil
	}
	return nil, "", fmt.Errorf("chat2019: all image ports failed: %w", lastErr)
}

func checkMediaPath(path string) error {
	// Paths from messages are filenames, never URLs; reject escapes as well to avoid
	// a second decoding step at the upstream turning a filename into traversal.
	if path == "" || strings.Contains(path, "..") || strings.Contains(path, "://") || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\%?#\r\n") {
		return errors.New("chat2019: invalid relative media path")
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" {
		return errors.New("chat2019: invalid relative media path")
	}
	return nil
}
func mediaHeaders(req *http.Request) {
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0")
}
