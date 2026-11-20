//go:build !e2e

package main

// Production always runs coordinated periodic maintenance; this is not a runtime setting.
const periodicJobsEnabled = true
