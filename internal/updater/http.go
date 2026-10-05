package updater

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const metadataLimit = 32 << 20

type HTTPError struct {
	StatusCode int
	URL        string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d from %s", e.StatusCode, e.URL) }

func NewClient(key string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Minute}, CurseForgeKey: key}
}

func (c *Client) request(ctx context.Context, method, address string, body io.Reader, headers map[string]string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, fmt.Errorf("refusing non-HTTPS URL %q", address)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcupdater/0.1 (Minecraft server administrator tool)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	// Never forward API credentials to another origin, including redirects.
	copyClient := *client
	previousRedirect := client.CheckRedirect
	copyClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" && next.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("refusing insecure redirect")
		}
		if next.URL.Host != via[0].URL.Host {
			next.Header.Del("x-api-key")
			next.Header.Del("Authorization")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	resp, err := copyClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, URL: address}
	}
	return resp, nil
}

func (c *Client) Get(ctx context.Context, address string) ([]byte, error) {
	resp, err := c.request(ctx, http.MethodGet, address, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, metadataLimit+1))
	if err == nil && len(data) > metadataLimit {
		err = fmt.Errorf("metadata from %s exceeds %d bytes", address, metadataLimit)
	}
	return data, err
}

func (c *Client) GetJSON(ctx context.Context, address string, out any) error {
	return c.JSON(ctx, http.MethodGet, address, nil, nil, out)
}
func (c *Client) JSON(ctx context.Context, method, address string, body any, headers map[string]string, out any) error {
	var reader io.Reader
	copied := map[string]string{"Accept": "application/json"}
	for k, v := range headers {
		copied[k] = v
	}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
		copied["Content-Type"] = "application/json"
	}
	resp, err := c.request(ctx, method, address, reader, copied)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, metadataLimit+1))
	if err != nil {
		return err
	}
	if len(data) > metadataLimit {
		return fmt.Errorf("metadata from %s exceeds limit", address)
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", address, err)
	}
	return nil
}

// Legacy upstream APIs publish SHA1/MD5, so compatibility checks must use those
// algorithms. Downloads require HTTPS; our installed-file records use SHA256.
// These checks do not treat an upstream digest as a cryptographic signature.
func artifactHasher(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	case "md5":
		return md5.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum algorithm %q", algorithm)
	}
}

func (c *Client) Download(ctx context.Context, a Artifact, dest string) (err error) {
	if a.URL == "" {
		return fmt.Errorf("empty artifact URL")
	}
	var h hash.Hash
	if a.Hash != "" {
		h, err = artifactHasher(a.HashAlgorithm)
		if err != nil {
			return err
		}
		digest, e := hex.DecodeString(a.Hash)
		if e != nil || len(digest) != h.Size() {
			return fmt.Errorf("invalid %s checksum", a.HashAlgorithm)
		}
	}
	resp, err := c.request(ctx, http.MethodGet, a.URL, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(dest)
		}
	}()
	var w io.Writer = f
	if h != nil {
		w = io.MultiWriter(f, h)
	}
	// Bound untrusted downloads even if the service omits a size.
	limit := int64(2 << 30)
	if a.Size > 0 {
		limit = a.Size
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit || (a.Size > 0 && n != a.Size) {
		return fmt.Errorf("download size mismatch for %s", a.Filename)
	}
	if h != nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.Hash) {
		return fmt.Errorf("checksum mismatch for %s", a.Filename)
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// fetchVerified downloads a small checksummed artifact into memory, so check can
// verify a live file without writing anything to the server directory.
func (c *Client) fetchVerified(ctx context.Context, a Artifact) ([]byte, error) {
	if a.Hash == "" {
		return nil, fmt.Errorf("refusing to trust %s without a published checksum", a.URL)
	}
	h, err := artifactHasher(a.HashAlgorithm)
	if err != nil {
		return nil, err
	}
	data, err := c.Get(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	if a.Size > 0 && int64(len(data)) != a.Size {
		return nil, fmt.Errorf("size mismatch for %s", a.URL)
	}
	h.Write(data)
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.Hash) {
		return nil, fmt.Errorf("checksum mismatch for %s", a.URL)
	}
	return data, nil
}
