package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

const (
	// Azure AD "common" tenant endpoint – works for both personal and work accounts.
	// When a tenant is supplied the per-tenant endpoint is used instead.
	aadDeviceCodeURL = "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode"
	aadTokenURL      = "https://login.microsoftonline.com/common/oauth2/v2.0/token"

	// Per-tenant endpoints (printf templates).
	aadDeviceCodeURLTmpl = "https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode"
	aadTokenURLTmpl      = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"

	// The well-known Azure CLI / ACR client-id that is whitelisted for public device-code flows.
	acrClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"

	// The scope needed to exchange an AAD token for an ACR token.
	acrScope = "https://management.azure.com/.default offline_access"
)

// acrDeviceCodeResponse is the JSON body returned by the AAD devicecode endpoint.
type acrDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

// aadTokenResponse is the JSON body returned when polling the token endpoint.
type aadTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// acrTokenResponse is the JSON body returned by the ACR token-exchange endpoint.
type acrTokenResponse struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
}

// acrAuthenticator implements authn.Authenticator for Azure Container Registry.
type acrAuthenticator struct {
	registry    string
	accessToken string
}

// Authorization returns the Bearer token for ACR.
func (a *acrAuthenticator) Authorization() (*authn.AuthConfig, error) {
	return &authn.AuthConfig{
		Username: "00000000-0000-0000-0000-000000000000",
		Password: a.accessToken,
	}, nil
}

// NewACRAuthenticator performs the browser-based (device-code) Azure AD login and
// exchanges the resulting AAD token for an ACR access token.
//
// If tenant is non-empty, the per-tenant AAD endpoint is used and the tenant id
// is forwarded to the ACR /oauth2/exchange call. This is required for ACRs that
// live in a tenant where the signed-in account is a guest.
func NewACRAuthenticator(registry, tenant string) (authn.Authenticator, error) {
	// Fast path: reuse an existing `az login` session if available.
	aadToken, err := getTokenFromAzCLI(tenant)
	if err != nil {
		fmt.Printf("az CLI cache unavailable (%v); falling back to device-code login\n", err)
		aadToken, err = getTokenViaDeviceCode(tenant)
		if err != nil {
			return nil, err
		}
	} else {
		fmt.Println("Reusing Azure CLI cached credentials.")
	}

	// Step 4 – Exchange the AAD access token for an ACR refresh token.
	acrRefreshToken, err := exchangeACRToken(registry, tenant, aadToken)
	if err != nil {
		return nil, fmt.Errorf("exchanging ACR token: %w", err)
	}

	// Step 5 – Get a scoped ACR access token from the refresh token.
	acrAccessToken, err := getACRAccessToken(registry, acrRefreshToken)
	if err != nil {
		return nil, fmt.Errorf("getting ACR access token: %w", err)
	}

	return &acrAuthenticator{registry: registry, accessToken: acrAccessToken}, nil
}

// getTokenFromAzCLI tries to obtain an AAD access token from a cached `az login`
// session. Returns an error if the az CLI is unavailable, the user isn't logged in,
// or the requested tenant doesn't match a cached subscription.
func getTokenFromAzCLI(tenant string) (string, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return "", fmt.Errorf("az CLI not found in PATH")
	}
	args := []string{"account", "get-access-token", "--resource", "https://management.azure.com", "--output", "json"}
	if tenant != "" {
		args = append(args, "--tenant", tenant)
	}
	cmd := exec.Command("az", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("az: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("az: %w", err)
	}
	var tr struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(out, &tr); err != nil {
		return "", fmt.Errorf("parsing az output: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("az returned empty accessToken")
	}
	return tr.AccessToken, nil
}

// getTokenViaDeviceCode is the original interactive AAD device-code flow.
func getTokenViaDeviceCode(tenant string) (string, error) {
	dcResp, err := requestDeviceCode(tenant)
	if err != nil {
		return "", fmt.Errorf("requesting device code: %w", err)
	}
	fmt.Printf("\nTo sign in to Azure, open a browser and visit:\n  %s\nand enter the code: %s\n\n",
		dcResp.VerificationURL, dcResp.UserCode)
	openBrowser(dcResp.VerificationURL)
	tok, err := pollForToken(dcResp.DeviceCode, dcResp.Interval, dcResp.ExpiresIn, tenant)
	if err != nil {
		return "", fmt.Errorf("waiting for Azure login: %w", err)
	}
	return tok, nil
}

func deviceCodeURL(tenant string) string {
	if tenant == "" {
		return aadDeviceCodeURL
	}
	return fmt.Sprintf(aadDeviceCodeURLTmpl, url.PathEscape(tenant))
}

func tokenURL(tenant string) string {
	if tenant == "" {
		return aadTokenURL
	}
	return fmt.Sprintf(aadTokenURLTmpl, url.PathEscape(tenant))
}

func requestDeviceCode(tenant string) (*acrDeviceCodeResponse, error) {
	resp, err := http.PostForm(deviceCodeURL(tenant), url.Values{
		"client_id": {acrClientID},
		"scope":     {acrScope},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, body)
	}
	var dc acrDeviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	return &dc, nil
}

func pollForToken(deviceCode string, intervalSecs, expiresSecs int, tenant string) (string, error) {
	if intervalSecs <= 0 {
		intervalSecs = 5
	}
	deadline := time.Now().Add(time.Duration(expiresSecs) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(intervalSecs) * time.Second)

		resp, err := http.PostForm(tokenURL(tenant), url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {acrClientID},
			"device_code": {deviceCode},
		})
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		var tr aadTokenResponse
		if err := json.Unmarshal(body, &tr); err != nil {
			return "", fmt.Errorf("parsing token response: %w", err)
		}

		switch tr.Error {
		case "":
			fmt.Println("Azure login successful.")
			return tr.AccessToken, nil
		case "authorization_pending":
			fmt.Print(".")
			continue
		case "slow_down":
			intervalSecs += 5
			continue
		default:
			return "", fmt.Errorf("AAD token error %q: %s", tr.Error, tr.ErrorDesc)
		}
	}
	return "", fmt.Errorf("device code expired before authentication completed")
}

func exchangeACRToken(registry, tenant, aadAccessToken string) (string, error) {
	exchangeURL := fmt.Sprintf("https://%s/oauth2/exchange", registry)
	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {registry},
		"access_token": {aadAccessToken},
	}
	if tenant != "" {
		form.Set("tenant", tenant)
	}
	resp, err := http.PostForm(exchangeURL, form)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ACR token exchange failed (%d): %s", resp.StatusCode, body)
	}
	var tr acrTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("parsing ACR exchange response: %w", err)
	}
	return tr.RefreshToken, nil
}

func getACRAccessToken(registry, acrRefreshToken string) (string, error) {
	tokenURL := fmt.Sprintf("https://%s/oauth2/token", registry)
	resp, err := http.PostForm(tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"service":       {registry},
		"scope":         {"repository:*:pull"},
		"refresh_token": {acrRefreshToken},
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ACR access token request failed (%d): %s", resp.StatusCode, body)
	}
	var tr acrTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("parsing ACR token response: %w", err)
	}
	return tr.AccessToken, nil
}

// openBrowser tries to open the verification URL in the default browser.
// It silently ignores errors because the user can always navigate manually.
func openBrowser(rawURL string) {
	// Validate URL before opening.
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	// Only open well-known Microsoft login domains.
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".microsoft.com") &&
		!strings.HasSuffix(host, ".microsoftonline.com") &&
		!strings.HasSuffix(host, ".live.com") {
		return
	}

	// Try platform-specific commands (Windows first, then Linux/macOS fallbacks).
	for _, candidate := range [][]string{
		{"cmd", "/c", "start", rawURL},
		{"rundll32", "url.dll,FileProtocolHandler", rawURL},
		{"xdg-open", rawURL},
		{"open", rawURL},
	} {
		if path, err := exec.LookPath(candidate[0]); err == nil {
			cmd := exec.Command(path, candidate[1:]...) //nolint:gosec
			_ = cmd.Start()
			return
		}
	}
}
