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

// TestAADScope verifies the scope constant required for ACR token exchange.
func TestAADScope(t *testing.T) {
	if !strings.Contains(aadScope, "management.azure.com") {
		t.Errorf("aadScope %q does not contain management.azure.com", aadScope)
	}
}

// TestACRAuthenticatorAuthorization confirms the sentinel username/password
// format expected by ACR.
func TestACRAuthenticatorAuthorization(t *testing.T) {
	a := &acrAuthenticator{accessToken: "test-token"}
	cfg, err := a.Authorization()
	if err != nil {
		t.Fatalf("Authorization: unexpected error: %v", err)
	}
	if cfg.Username != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("Username: got %q, want sentinel UUID", cfg.Username)
	}
	if cfg.Password != "test-token" {
		t.Errorf("Password: got %q, want %q", cfg.Password, "test-token")
	}
}

// TestNewAzureCredential ensures the credential chain builds without error.
func TestNewAzureCredential(t *testing.T) {
	if _, err := newAzureCredential(); err != nil {
		t.Errorf("newAzureCredential: unexpected error: %v", err)
	}
}
