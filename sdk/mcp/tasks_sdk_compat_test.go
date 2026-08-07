package main

import "testing"

func TestTasksSDKCompatConstants(t *testing.T) {
	if tasksSDKHasFirstClassSupport {
		t.Fatal("go-sdk v1.7.0 has no first-class Tasks; flag must stay false until upgrade")
	}
	if tasksMethodGet != "tasks/get" || tasksMethodUpdate != "tasks/update" || tasksMethodCancel != "tasks/cancel" {
		t.Fatalf("unexpected method names: %s %s %s", tasksMethodGet, tasksMethodUpdate, tasksMethodCancel)
	}
}
