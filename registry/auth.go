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
	aadDeviceCodeURL = "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode"
	aadTokenURL      = "https://login.microsoftonline.com/common/oauth2/v2.0/token"

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
func NewACRAuthenticator(registry string) (authn.Authenticator, error) {
	// Step 1 – Request a device code.
	dcResp, err := requestDeviceCode()
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}

	// Step 2 – Show the code + URL to the user and try to open the browser.
	fmt.Printf("\nTo sign in to Azure, open a browser and visit:\n  %s\nand enter the code: %s\n\n",
		dcResp.VerificationURL, dcResp.UserCode)
	openBrowser(dcResp.VerificationURL)

	// Step 3 – Poll for the AAD token.
	aadToken, err := pollForToken(dcResp.DeviceCode, dcResp.Interval, dcResp.ExpiresIn)
	if err != nil {
		return nil, fmt.Errorf("waiting for Azure login: %w", err)
	}

	// Step 4 – Exchange the AAD access token for an ACR refresh token.
	acrRefreshToken, err := exchangeACRToken(registry, aadToken)
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

func requestDeviceCode() (*acrDeviceCodeResponse, error) {
	resp, err := http.PostForm(aadDeviceCodeURL, url.Values{
		"client_id": {acrClientID},
		"scope":     {acrScope},
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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

func pollForToken(deviceCode string, intervalSecs, expiresSecs int) (string, error) {
	if intervalSecs <= 0 {
		intervalSecs = 5
	}
	deadline := time.Now().Add(time.Duration(expiresSecs) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(intervalSecs) * time.Second)

		resp, err := http.PostForm(aadTokenURL, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {acrClientID},
			"device_code": {deviceCode},
		})
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

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

func exchangeACRToken(registry, aadAccessToken string) (string, error) {
	exchangeURL := fmt.Sprintf("https://%s/oauth2/exchange", registry)
	resp, err := http.PostForm(exchangeURL, url.Values{
		"grant_type":   {"access_token"},
		"service":      {registry},
		"access_token": {aadAccessToken},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
