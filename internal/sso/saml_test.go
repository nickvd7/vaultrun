package sso

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/crewjam/saml"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}

func TestLoadIDPMetadataFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idp_metadata.xml")
	if err := os.WriteFile(path, readTestdata(t, "idp_metadata.xml"), 0o644); err != nil {
		t.Fatalf("write metadata file: %v", err)
	}

	meta, err := loadIDPMetadata(t.Context(), "https://ignored.example.com/metadata", path)
	if err != nil {
		t.Fatalf("loadIDPMetadata: %v", err)
	}
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}
	if meta.EntityID == "" {
		t.Error("expected non-empty EntityID from parsed metadata")
	}
}

func TestLoadIDPMetadataFromFileMissing(t *testing.T) {
	_, err := loadIDPMetadata(t.Context(), "", filepath.Join(t.TempDir(), "does-not-exist.xml"))
	if err == nil {
		t.Fatal("expected error for missing metadata file")
	}
}

func TestLoadIDPMetadataFromFileInvalidXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(path, []byte("not valid xml at all"), 0o644); err != nil {
		t.Fatalf("write bad metadata file: %v", err)
	}

	if _, err := loadIDPMetadata(t.Context(), "", path); err == nil {
		t.Fatal("expected error for invalid metadata XML")
	}
}

func TestLoadIDPMetadataFromURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(readTestdata(t, "idp_metadata.xml"))
	}))
	t.Cleanup(srv.Close)

	meta, err := loadIDPMetadata(t.Context(), srv.URL, "")
	if err != nil {
		t.Fatalf("loadIDPMetadata: %v", err)
	}
	if meta == nil || meta.EntityID == "" {
		t.Fatal("expected metadata with EntityID from URL fetch")
	}
}

func TestLoadIDPMetadataFileTakesPrecedenceOverURL(t *testing.T) {
	// A URL that would fail if it were actually fetched; since a file path is
	// also given, the file must be preferred (avoids live-URL MITM risk).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "idp_metadata.xml")
	if err := os.WriteFile(path, readTestdata(t, "idp_metadata.xml"), 0o644); err != nil {
		t.Fatalf("write metadata file: %v", err)
	}

	meta, err := loadIDPMetadata(t.Context(), srv.URL, path)
	if err != nil {
		t.Fatalf("expected file to be used instead of failing URL, got error: %v", err)
	}
	if meta == nil || meta.EntityID == "" {
		t.Fatal("expected metadata parsed from file")
	}
}

func TestLoadIDPMetadataNeitherConfigured(t *testing.T) {
	if _, err := loadIDPMetadata(t.Context(), "", ""); err == nil {
		t.Fatal("expected error when neither metadata file nor URL is configured")
	}
}

func TestLoadIDPMetadataURLNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	if _, err := loadIDPMetadata(t.Context(), srv.URL, ""); err == nil {
		t.Fatal("expected error for non-200 metadata URL response")
	}
}

func TestAttrValues(t *testing.T) {
	attr := saml.Attribute{
		Name: "email",
		Values: []saml.AttributeValue{
			{Value: "user@example.com"},
			{Value: ""},
			{Value: "second@example.com"},
		},
	}
	got := attrValues(attr)
	want := []string{"user@example.com", "second@example.com"}
	if len(got) != len(want) {
		t.Fatalf("attrValues = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attrValues[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAttrValuesEmpty(t *testing.T) {
	attr := saml.Attribute{Name: "empty", Values: []saml.AttributeValue{{Value: ""}}}
	if got := attrValues(attr); len(got) != 0 {
		t.Errorf("expected no values, got %v", got)
	}
}
