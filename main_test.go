package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReadCappedBodySmall(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("ok"))}
	body, tooLarge, err := readCappedBody(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tooLarge {
		t.Fatalf("expected tooLarge=false")
	}
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

func TestReadCappedBodyAtLimit(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("a"), maxResponseBytes)))}
	body, tooLarge, err := readCappedBody(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tooLarge {
		t.Fatalf("expected tooLarge=true")
	}
	if len(body) != maxResponseBytes {
		t.Fatalf("unexpected body length: %d", len(body))
	}
}

func TestReadCappedBodyOverLimit(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("b"), maxResponseBytes+10)))}
	body, tooLarge, err := readCappedBody(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tooLarge {
		t.Fatalf("expected tooLarge=true")
	}
	if len(body) != maxResponseBytes {
		t.Fatalf("unexpected body length: %d", len(body))
	}
}

func TestFormatSnippetTooLarge(t *testing.T) {
	if got := formatSnippet([]byte("error"), true); got != "response exceeded 1MB limit" {
		t.Fatalf("unexpected snippet: %q", got)
	}
}

func TestFormatSnippetEmpty(t *testing.T) {
	if got := formatSnippet([]byte(" \n\t"), false); got != "empty response body" {
		t.Fatalf("unexpected snippet: %q", got)
	}
}

func TestFormatSnippetTruncates(t *testing.T) {
	body := strings.Repeat("a", responseSnippetBytes+50)
	if got := formatSnippet([]byte(body), false); len(got) != responseSnippetBytes {
		t.Fatalf("unexpected snippet length: %d", len(got))
	}
}

func TestListStationsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"GetStationsResult":{"Stations":[{"ID":1,"Name":"Alpha"},{"ID":2,"Name":"Beta"}]}}`)
	}))
	defer server.Close()

	originalURL := stationsListURL
	stationsListURL = server.URL
	defer func() { stationsListURL = originalURL }()

	output := captureStdout(t, func() {
		httpClient := &http.Client{}
		if err := listStations(httpClient); err != nil {
			t.Fatalf("listStations error: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	seen := map[string]string{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("unexpected line format: %q", line)
		}
		seen[fields[0]] = fields[1]
	}
	if seen["1"] != "Alpha" || seen["2"] != "Beta" {
		t.Fatalf("unexpected output: %v", seen)
	}
}

func TestListStationsNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request")
	}))
	defer server.Close()

	originalURL := stationsListURL
	stationsListURL = server.URL
	defer func() { stationsListURL = originalURL }()

	httpClient := &http.Client{}
	if err := listStations(httpClient); err == nil {
		t.Fatalf("expected error for non-2xx response")
	}
}

func TestListStationsTooLarge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload := strings.Repeat("a", maxResponseBytes+10)
		fmt.Fprint(w, payload)
	}))
	defer server.Close()

	originalURL := stationsListURL
	stationsListURL = server.URL
	defer func() { stationsListURL = originalURL }()

	httpClient := &http.Client{}
	if err := listStations(httpClient); err == nil {
		t.Fatalf("expected error for oversized response")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	rescue := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	defer func() {
		os.Stdout = rescue
		_ = r.Close()
	}()
	os.Stdout = w

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read error: %v", err)
	}
	return buf.String()
}
