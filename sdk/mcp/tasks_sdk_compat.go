// Upstream Go SDK Tasks compatibility notes and registration boundary.
//
// As of github.com/modelcontextprotocol/go-sdk v1.7.0 the SDK speaks MCP
// 2026-07-28 and exposes AddReceivingCustomMethod, but it does NOT ship
// first-class helpers for the Tasks extension (io.modelcontextprotocol/tasks,
// SEP-2663): no Task types, no tasks/get|update|cancel registration helpers,
// and no built-in async tool result wrapping.
//
// VaultRun therefore keeps a server-side implementation in tasks.go /
// tasks_input.go / tasks_redis.go and registers the wire methods through
// registerTaskMethods. When upstream adds Tasks support, migrate by:
//
//  1. Prefer SDK-native registration / types if they cover get/update/cancel
//     and CreateTaskResult (resultType:"task").
//  2. Keep VaultRun persistence (memory/Redis), security caps, owner binding,
//     and input_required/confirm behavior behind the same taskStore.
//  3. Delete or narrow this file once the SDK path is the only registration
//     surface and sdk_ext_test.go still passes.
//
// Do not invent a parallel Tasks protocol here — this file is only the
// documented seam between VaultRun's store and the SDK custom-method API.
package main

// Tasks extension JSON-RPC methods (SEP-2663). Kept as named constants so
// call sites and future SDK migration stay grep-friendly.
const (
	tasksMethodGet    = "tasks/get"
	tasksMethodUpdate = "tasks/update"
	tasksMethodCancel = "tasks/cancel"
)

// tasksSDKHasFirstClassSupport is false for go-sdk v1.7.0. Flip (or delete)
// when upgrading to an SDK release that documents Tasks helpers — tests can
// gate optional migration paths on this flag.
const tasksSDKHasFirstClassSupport = false
