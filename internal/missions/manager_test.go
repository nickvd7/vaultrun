package missions

import "testing"

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
