package metrics

// Default is the process-wide registry, set by main.go at startup.
// Instrumentation sites read it lazily; nil means metrics are disabled
// (e.g. during tests that don't set it up).
var Default *Registry
