package missions

import (
	"strings"
	"testing"
)

func TestCreateMissionRequestValidate(t *testing.T) {
	err := (CreateMissionRequest{
		Slug: "Bad Slug", Name: "x", Steps: []Step{{Tool: "run_command"}},
	}).Validate()
	if err == nil {
		t.Fatal("expected invalid slug")
	}
	err = (CreateMissionRequest{
		Slug: "ok-slug", Name: "Demo", Steps: []Step{{Tool: "run_command", Args: map[string]string{"command": "echo"}}},
	}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	err = (CreateMissionRequest{Slug: "ok", Name: "x", Steps: nil}).Validate()
	if err == nil {
		t.Fatal("expected steps required")
	}
}

func TestCreateMissionNormalize(t *testing.T) {
	req := CreateMissionRequest{
		Slug: "  ok-slug  ", Name: "  Demo  ",
		Steps: []Step{{Tool: "run_command"}},
	}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	req.Normalize()
	if req.Slug != "ok-slug" || req.Name != "Demo" {
		t.Fatalf("got slug=%q name=%q", req.Slug, req.Name)
	}
}

func TestValidateRejectsOversizedArgsAndTags(t *testing.T) {
	big := strings.Repeat("a", maxArgValueLen+1)
	err := (CreateMissionRequest{
		Slug: "ok", Name: "n",
		Steps: []Step{{Tool: "run_command", Args: map[string]string{"x": big}}},
	}).Validate()
	if err == nil {
		t.Fatal("expected oversized args rejected")
	}
	tags := make([]string, maxTags+1)
	for i := range tags {
		tags[i] = "t"
	}
	err = (CreateMissionRequest{
		Slug: "ok", Name: "n", Tags: tags,
		Steps: []Step{{Tool: "run_command"}},
	}).Validate()
	if err == nil {
		t.Fatal("expected too many tags rejected")
	}
}

func TestValidateVersionLength(t *testing.T) {
	err := (CreateMissionRequest{
		Slug: "ok", Name: "n", Version: strings.Repeat("1", maxVersionLen+1),
		Steps: []Step{{Tool: "run_command"}},
	}).Validate()
	if err == nil {
		t.Fatal("expected version too long")
	}
}
