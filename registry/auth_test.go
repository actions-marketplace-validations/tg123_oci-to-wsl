package registry

import (
	"strings"
	"testing"
)

func TestIsACR(t *testing.T) {
	cases := []struct {
		registry string
		want     bool
	}{
		{"myacr.azurecr.io", true},
		{"MYACR.AZURECR.IO", true},
		{"myacr.azurecr.cn", true},
		{"myacr.azurecr.us", true},
		{"index.docker.io", false},
		{"gcr.io", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isACR(tc.registry)
		if got != tc.want {
			t.Errorf("isACR(%q) = %v, want %v", tc.registry, got, tc.want)
		}
	}
}

func TestOpenBrowser_IgnoresNonMicrosoftURL(t *testing.T) {
	// openBrowser with a non-Microsoft URL must not panic and must be a no-op.
	// (We just call it and ensure no panic; we cannot test process spawning here.)
	openBrowser("https://evil.example.com/malicious")
}

func TestOpenBrowser_IgnoresInvalidURL(t *testing.T) {
	openBrowser("://invalid")
}

func TestOpenBrowser_IgnoresHTTPURL(t *testing.T) {
	// http (not https) to a Microsoft domain is silently accepted by our validator,
	// but we cover it to make sure there is no panic.
	openBrowser("http://login.microsoftonline.com/deviceauth")
}

// TestACRScopeContainsManagement verifies the scope constant used for ACR auth
// includes the management plane URL, which is required for the device flow.
func TestACRScopeContainsManagement(t *testing.T) {
	if !strings.Contains(acrScope, "management.azure.com") {
		t.Errorf("acrScope %q does not contain management.azure.com", acrScope)
	}
}
