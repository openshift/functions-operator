package funccli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

// DownloadOptions configures the binary download behavior.
type DownloadOptions struct {
	// HTTPClient is the HTTP client to use for downloading.
	// If nil, a default client with a 30-second timeout is used.
	HTTPClient *http.Client
}

func (o *DownloadOptions) httpClient() *http.Client {
	if o != nil && o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// AssetName returns the func CLI asset name for the current platform
// (e.g. "func_linux_amd64").
func AssetName() string {
	return fmt.Sprintf("func_%s_%s", goruntime.GOOS, goruntime.GOARCH)
}

// DownloadAndInstall downloads a func CLI binary from binaryURL, verifies its
// SHA256 checksum against the checksums.txt at checksumURL, and atomically
// installs it to installPath. The parent directory of installPath must exist.
func DownloadAndInstall(ctx context.Context, binaryURL, checksumURL, installPath string, opts *DownloadOptions) error {
	client := opts.httpClient()
	assetName := filepath.Base(binaryURL)

	tmpFile := installPath + ".tmp"
	if err := downloadToFile(ctx, client, binaryURL, tmpFile); err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}

	if err := verifyFileChecksum(ctx, client, tmpFile, assetName, checksumURL); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	if err := os.Chmod(tmpFile, 0o755); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	if err := os.Rename(tmpFile, installPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

func downloadToFile(ctx context.Context, client *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func verifyFileChecksum(ctx context.Context, client *http.Client, filePath, assetName, checksumURL string) error {
	expectedHash, err := fetchExpectedChecksum(ctx, client, assetName, checksumURL)
	if err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

func fetchExpectedChecksum(ctx context.Context, client *http.Client, assetName, checksumURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", checksumURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums download failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read checksums: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			return parts[0], nil
		}
	}

	return "", fmt.Errorf("no checksum found for asset %s", assetName)
}
