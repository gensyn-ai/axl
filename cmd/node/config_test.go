package main

import "testing"

// TestApplyOverridesA2APort guards against a regression where applyOverrides
// silently drops the a2a_port field. Without the matching clause in
// applyOverrides, JSON-supplied a2a_port values are parsed but never copied
// onto the base config, leaving every node bound to the default 9004.
func TestApplyOverridesA2APort(t *testing.T) {
	base := DefaultAPIConfig()
	ov := ApiConfig{A2APort: 9999}
	applyOverrides(&base, ov)
	if base.A2APort != 9999 {
		t.Errorf("a2a_port override dropped: got %d, want 9999", base.A2APort)
	}
}

// TestApplyOverridesAllFields exercises every overridable field once so a
// future field addition that forgets its applyOverrides clause fails loudly.
func TestApplyOverridesAllFields(t *testing.T) {
	base := DefaultAPIConfig()
	ov := ApiConfig{
		ApiPort:         8888,
		McpRouterPort:   9991,
		A2APort:         9992,
		McpRouterAddr:   "http://router.example",
		BridgeAddr:      "127.0.0.2",
		A2AAddr:         "http://a2a.example",
		MaxMessageSize:  1234,
		MaxConcConns:    42,
		ConnReadTimeout: 11,
		ConnIdleTimeout: 22,
	}
	applyOverrides(&base, ov)

	checks := []struct {
		name     string
		got, want any
	}{
		{"ApiPort", base.ApiPort, ov.ApiPort},
		{"McpRouterPort", base.McpRouterPort, ov.McpRouterPort},
		{"A2APort", base.A2APort, ov.A2APort},
		{"McpRouterAddr", base.McpRouterAddr, ov.McpRouterAddr},
		{"BridgeAddr", base.BridgeAddr, ov.BridgeAddr},
		{"A2AAddr", base.A2AAddr, ov.A2AAddr},
		{"MaxMessageSize", base.MaxMessageSize, ov.MaxMessageSize},
		{"MaxConcConns", base.MaxConcConns, ov.MaxConcConns},
		{"ConnReadTimeout", base.ConnReadTimeout, ov.ConnReadTimeout},
		{"ConnIdleTimeout", base.ConnIdleTimeout, ov.ConnIdleTimeout},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s override dropped: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestApplyOverridesZeroValuesPreserveDefaults asserts that a zero/empty
// override leaves the base default intact (i.e. the `if != 0` / `if != ""`
// guards work as intended).
func TestApplyOverridesZeroValuesPreserveDefaults(t *testing.T) {
	base := DefaultAPIConfig()
	want := base
	applyOverrides(&base, ApiConfig{})
	if base != want {
		t.Errorf("zero override mutated base: got %+v, want %+v", base, want)
	}
}
