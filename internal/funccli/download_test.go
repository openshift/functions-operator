package funccli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAssetName(t *testing.T) {
	expected := fmt.Sprintf("func_%s_%s", runtime.GOOS, runtime.GOARCH)
	if got := AssetName(); got != expected {
		t.Errorf("AssetName() = %q, want %q", got, expected)
	}
}

func newTestServer(binaryContent []byte, checksumContent string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binaryContent)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksumContent))
	})
	return httptest.NewServer(mux)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func TestDownloadAndInstall_Success(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho hello")
	hash := sha256Hex(binaryContent)
	checksums := fmt.Sprintf("%s  binary\n", hash)

	srv := newTestServer(binaryContent, checksums)
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: srv.Client()},
	)
	if err != nil {
		t.Fatalf("DownloadAndInstall() error = %v", err)
	}

	got, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("reading installed file: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("installed content = %q, want %q", got, binaryContent)
	}

	info, err := os.Stat(installPath)
	if err != nil {
		t.Fatalf("stat installed file: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed file is not executable, mode = %v", info.Mode())
	}

	tmpFile := installPath + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Errorf("temp file %s should not exist after successful install", tmpFile)
	}
}

func TestDownloadAndInstall_ChecksumMismatch(t *testing.T) {
	binaryContent := []byte("real binary")
	checksums := "0000000000000000000000000000000000000000000000000000000000000000  binary\n"

	srv := newTestServer(binaryContent, checksums)
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: srv.Client()},
	)
	if err == nil {
		t.Fatal("expected error for checksum mismatch, got nil")
	}

	if _, statErr := os.Stat(installPath); !os.IsNotExist(statErr) {
		t.Error("install path should not exist after checksum mismatch")
	}

	tmpFile := installPath + ".tmp"
	if _, statErr := os.Stat(tmpFile); !os.IsNotExist(statErr) {
		t.Error("temp file should be cleaned up after checksum mismatch")
	}
}

func TestDownloadAndInstall_BinaryDownloadFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abc  binary\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: srv.Client()},
	)
	if err == nil {
		t.Fatal("expected error for binary download failure, got nil")
	}
}

func TestDownloadAndInstall_ChecksumDownloadFails(t *testing.T) {
	binaryContent := []byte("binary data")

	mux := http.NewServeMux()
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binaryContent)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: srv.Client()},
	)
	if err == nil {
		t.Fatal("expected error for checksum download failure, got nil")
	}

	tmpFile := installPath + ".tmp"
	if _, statErr := os.Stat(tmpFile); !os.IsNotExist(statErr) {
		t.Error("temp file should be cleaned up after checksum download failure")
	}
}

func TestDownloadAndInstall_CustomHTTPClient(t *testing.T) {
	clientUsed := false

	binaryContent := []byte("test binary")
	hash := sha256Hex(binaryContent)
	checksums := fmt.Sprintf("%s  binary\n", hash)

	srv := newTestServer(binaryContent, checksums)
	defer srv.Close()

	customTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clientUsed = true
		return http.DefaultTransport.RoundTrip(req)
	})

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: &http.Client{Transport: customTransport}},
	)
	if err != nil {
		t.Fatalf("DownloadAndInstall() error = %v", err)
	}

	if !clientUsed {
		t.Error("custom HTTP client was not used")
	}
}

func TestDownloadAndInstall_NilOptions(t *testing.T) {
	binaryContent := []byte("test binary")
	hash := sha256Hex(binaryContent)
	checksums := fmt.Sprintf("%s  binary\n", hash)

	srv := newTestServer(binaryContent, checksums)
	defer srv.Close()

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		context.Background(),
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		nil,
	)
	if err != nil {
		t.Fatalf("DownloadAndInstall(opts=nil) error = %v", err)
	}

	if _, err := os.Stat(installPath); err != nil {
		t.Errorf("installed file should exist: %v", err)
	}
}

func TestDownloadAndInstall_ContextCancelled(t *testing.T) {
	binaryContent := []byte("test binary")
	hash := sha256Hex(binaryContent)
	checksums := fmt.Sprintf("%s  binary\n", hash)

	srv := newTestServer(binaryContent, checksums)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	// ensure context is expired
	time.Sleep(1 * time.Millisecond)

	dir := t.TempDir()
	installPath := filepath.Join(dir, "func")

	err := DownloadAndInstall(
		ctx,
		srv.URL+"/binary",
		srv.URL+"/checksums.txt",
		installPath,
		&DownloadOptions{HTTPClient: srv.Client()},
	)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
