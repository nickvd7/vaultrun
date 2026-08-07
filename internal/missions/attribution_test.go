package missions

import "testing"

func TestUpdateRunRequestDefaults(t *testing.T) {
	req := UpdateRunRequest{Status: "completed", Attribute: true}
	if req.Status != "completed" || !req.Attribute {
		t.Fatalf("unexpected %+v", req)
	}
}

func TestCostAttributionJSONShape(t *testing.T) {
	a := CostAttribution{TotalCost: 1.25, MetricCount: 3}
	if a.TotalCost != 1.25 || a.MetricCount != 3 {
		t.Fatal(a)
	}
}
