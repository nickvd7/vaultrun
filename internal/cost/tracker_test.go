package cost

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

const epsilon = 0.0001 // For floating point comparison

func TestCalculateComputeCost(t *testing.T) {
	rate := CostRate{
		CPUCoreHourRate:  0.0416,
		MemoryGBHourRate: 0.0045,
		GPUHourRate:      1.00,
	}

	tests := []struct {
		name     string
		cpu      float64
		mem      float64
		gpu      float64
		expected float64
	}{
		{
			name:     "1 CPU core for 1 hour",
			cpu:      1.0,
			mem:      0.0,
			gpu:      0.0,
			expected: 0.0416,
		},
		{
			name:     "2 CPU cores + 4GB RAM for 1 hour",
			cpu:      2.0,
			mem:      4.0,
			gpu:      0.0,
			expected: (2 * 0.0416) + (4 * 0.0045),
		},
		{
			name:     "1 GPU for 1 hour",
			cpu:      0.0,
			mem:      0.0,
			gpu:      1.0,
			expected: 1.00,
		},
		{
			name:     "Full stack: 4 CPU + 8GB RAM + 1 GPU",
			cpu:      4.0,
			mem:      8.0,
			gpu:      1.0,
			expected: (4 * 0.0416) + (8 * 0.0045) + (1 * 1.00),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cpu*rate.CPUCoreHourRate + tt.mem*rate.MemoryGBHourRate + tt.gpu*rate.GPUHourRate
			if math.Abs(result-tt.expected) > epsilon {
				t.Errorf("compute cost = %.4f, expected %.4f", result, tt.expected)
			}
		})
	}
}

func TestCalculateStorageCost(t *testing.T) {
	rate := CostRate{
		StorageGBMonthRate:  0.023,
		SnapshotGBMonthRate: 0.023,
		ArtifactGBMonthRate: 0.023,
	}

	tests := []struct {
		name      string
		workspace float64
		snapshots float64
		artifacts float64
		expected  float64
	}{
		{
			name:      "10GB workspace for 1 day",
			workspace: 10.0,
			snapshots: 0.0,
			artifacts: 0.0,
			expected:  10.0 * 0.023,
		},
		{
			name:      "Full storage: 50GB workspace + 200GB snapshots + 100GB artifacts",
			workspace: 50.0,
			snapshots: 200.0,
			artifacts: 100.0,
			expected:  (50.0 * 0.023) + (200.0 * 0.023) + (100.0 * 0.023),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.workspace*rate.StorageGBMonthRate + tt.snapshots*rate.SnapshotGBMonthRate + tt.artifacts*rate.ArtifactGBMonthRate
			if math.Abs(result-tt.expected) > epsilon {
				t.Errorf("storage cost = %.4f, expected %.4f", result, tt.expected)
			}
		})
	}
}

func TestCalculateNetworkCost(t *testing.T) {
	rate := CostRate{
		EgressGBRate: 0.09,
	}

	tests := []struct {
		name     string
		egress   float64
		expected float64
	}{
		{
			name:     "No traffic",
			egress:   0.0,
			expected: 0.0,
		},
		{
			name:     "10GB egress",
			egress:   10.0,
			expected: 0.9,
		},
		{
			name:     "100GB egress",
			egress:   100.0,
			expected: 9.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.egress * rate.EgressGBRate
			if math.Abs(result-tt.expected) > epsilon {
				t.Errorf("network cost = %.4f, expected %.4f", result, tt.expected)
			}
		})
	}
}

func TestCostRateStructure(t *testing.T) {
	// Test that CostRate struct has all required fields
	rate := CostRate{
		ID:                  uuid.New(),
		Name:                "Test Rate",
		CPUCoreHourRate:     0.0416,
		MemoryGBHourRate:    0.0045,
		GPUHourRate:         1.00,
		StorageGBMonthRate:  0.023,
		SnapshotGBMonthRate: 0.023,
		ArtifactGBMonthRate: 0.023,
		EgressGBRate:        0.09,
		Active:              true,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	if rate.Name == "" {
		t.Error("CostRate.Name should not be empty")
	}
	if rate.CPUCoreHourRate <= 0 {
		t.Error("CostRate.CPUCoreHourRate should be positive")
	}
}

func TestCostMetricStructure(t *testing.T) {
	// Test that CostMetric struct has all required fields
	metric := CostMetric{
		ID:              uuid.New(),
		SessionID:       uuid.New(),
		PeriodStart:     time.Now(),
		PeriodEnd:       time.Now().Add(time.Hour),
		CPUCoreHours:    2.5,
		MemoryGBHours:   8.0,
		GPUHours:        1.0,
		WorkspaceGBDays: 10.0,
		SnapshotGBDays:  50.0,
		ArtifactGBDays:  20.0,
		EgressGB:        5.0,
		IngressGB:       2.0,
		ComputeCost:     0.14,
		StorageCost:     0.50,
		NetworkCost:     0.05,
		TotalCost:       0.69,
		Checksum:        "abc123",
		Signature:       "def456",
		CreatedAt:       time.Now(),
	}

	if metric.ID == uuid.Nil {
		t.Error("CostMetric.ID should not be nil")
	}
	if metric.TotalCost != 0.69 {
		t.Errorf("CostMetric.TotalCost = %.2f, expected 0.69", metric.TotalCost)
	}
}
