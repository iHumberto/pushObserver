// Package deploy orchestrates the full deployment pipeline.
//
// Pipeline: lock → git pull → detect changes → docker compose up → notify → unlock.
// Uses mutex per hook ID to serialize deployments.
package deploy

// TODO: Engine struct, New(), Deploy(hookID) — the main orchestrator
