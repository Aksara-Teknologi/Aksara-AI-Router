package pluginoauth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// AntigravityUserAgent identifies the current Hub client family. Google gates
// some Code Assist models on this family and client version, so keep this in
// sync for OAuth setup and inference requests.
const AntigravityUserAgent = "antigravity/hub/2.24.0 darwin/arm64"

// ProviderConfig pins installed-client endpoints and callbacks. Provider data is
// based on decolua/9router (MIT), revision 1339107e624d1ab150c6a0c2c2ee22caa7e9db2f.
func ProviderConfig(provider string) (*Config, error) {
	config := &Config{Provider: provider}
	switch provider {
	case "codex":
		config.AuthorizationURL = "https://auth.openai.com/oauth/authorize"
		config.TokenURL = "https://auth.openai.com/oauth/token"
		config.ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
		config.Scopes = []string{"openid", "profile", "email", "offline_access"}
		config.RedirectURI = "http://localhost:1455/auth/callback"
		config.AuthorizationParams = map[string]string{"id_token_add_organizations": "true", "codex_cli_simplified_flow": "true", "originator": "codex_cli_rs"}
	case "claude":
		config.AuthorizationURL = "https://claude.ai/oauth/authorize"
		config.TokenURL = "https://api.anthropic.com/v1/oauth/token"
		config.ClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
		config.Scopes = []string{"org:create_api_key", "user:profile", "user:inference"}
		config.RedirectURI = "http://localhost:54545/callback"
		config.TokenEncoding = "json"
		config.AuthorizationParams = map[string]string{"code": "true"}
	case "antigravity":
		config.AuthorizationURL = "https://accounts.google.com/o/oauth2/v2/auth"
		config.TokenURL = "https://oauth2.googleapis.com/token"
		config.ClientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
		config.Scopes = []string{"https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/cclog", "https://www.googleapis.com/auth/experimentsandconfigs"}
		config.RedirectURI = "http://localhost:51121/callback"
		config.AuthorizationParams = map[string]string{"access_type": "offline", "prompt": "consent"}
	default:
		return nil, ErrConfiguration
	}
	return config, nil
}

func (config Config) ValidateProvider() error {
	if config.Provider == "" {
		return nil
	}
	preset, err := ProviderConfig(config.Provider)
	if err != nil || config.Hash() != preset.Hash() {
		return ErrConfiguration
	}
	return nil
}

func (config Config) PrepareTokenValues(values url.Values) {
	if config.Provider == "antigravity" {
		// Installed application credential, published by the upstream native
		// client. This is not a user secret and is never injected into JS.
		values.Set("client_secret", "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")
	}
}

// CompleteProvider obtains only the routing metadata needed by inference.
// Tokens stay in the encrypted credential and are not included in public status.
func CompleteProvider(ctx context.Context, client *http.Client, config Config, credential *Credential) error {
	if config.Provider == "codex" {
		parts := strings.Split(credential.AccessToken, ".")
		if len(parts) != 3 {
			return ErrCredential
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ErrCredential
		}
		var claims struct {
			Email string `json:"email"`
			Auth  struct {
				AccountID string `json:"chatgpt_account_id"`
			} `json:"https://api.openai.com/auth"`
		}
		if common.Unmarshal(payload, &claims) != nil || claims.Auth.AccountID == "" || len(claims.Auth.AccountID) > 256 || strings.ContainsAny(claims.Auth.AccountID, "\r\n") {
			return ErrCredential
		}
		// A claim is routing data, not identity verification or authorization.
		credential.AccountID = claims.Auth.AccountID
		credential.AccountLabel = claims.Email
	}
	if config.Provider != "antigravity" {
		return nil
	}
	endpoint := "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	body := `{"metadata":{"ideType":9,"platform":0,"pluginType":2}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return ErrConfiguration
	}
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", AntigravityUserAgent)
	bounded := *client
	bounded.Timeout = 20 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(request)
	if err != nil {
		return ErrCredential
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrCredential
	}
	var result struct {
		Project any `json:"cloudaicompanionProject"`
		Tiers   []struct {
			ID      string `json:"id"`
			Default bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if common.DecodeJson(http.MaxBytesReader(nil, response.Body, 1<<20), &result) != nil {
		return ErrCredential
	}
	switch project := result.Project.(type) {
	case string:
		credential.ProjectID = project
	case map[string]any:
		credential.ProjectID, _ = project["id"].(string)
	}
	if credential.ProjectID == "" {
		tier := "legacy-tier"
		for _, allowed := range result.Tiers {
			if allowed.Default && allowed.ID != "" {
				tier = allowed.ID
				break
			}
		}
		payload, err := common.Marshal(map[string]any{"tierId": tier, "metadata": map[string]any{"ideType": 9, "platform": 0, "pluginType": 2}})
		if err != nil {
			return ErrCredential
		}
		for range 5 {
			onboard, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://cloudcode-pa.googleapis.com/v1internal:onboardUser", strings.NewReader(string(payload)))
			if err != nil {
				return ErrCredential
			}
			onboard.Header = request.Header.Clone()
			reply, err := bounded.Do(onboard)
			if err != nil {
				return ErrCredential
			}
			var operation struct {
				Done     bool `json:"done"`
				Response struct {
					Project any `json:"cloudaicompanionProject"`
				} `json:"response"`
			}
			decodeErr := common.DecodeJson(http.MaxBytesReader(nil, reply.Body, 1<<20), &operation)
			reply.Body.Close()
			if reply.StatusCode != http.StatusOK || decodeErr != nil {
				return ErrProviderProject
			}
			if operation.Done {
				switch project := operation.Response.Project.(type) {
				case string:
					credential.ProjectID = project
				case map[string]any:
					credential.ProjectID, _ = project["id"].(string)
				}
				break
			}
			select {
			case <-ctx.Done():
				return ErrProviderProject
			case <-time.After(time.Second):
			}
		}
	}
	if credential.ProjectID == "" || len(credential.ProjectID) > 256 {
		return ErrProviderProject
	}
	profileRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v1/userinfo?alt=json", nil)
	if err != nil {
		return ErrCredential
	}
	profileRequest.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	if profileResponse, err := bounded.Do(profileRequest); err == nil {
		var profile struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}
		if profileResponse.StatusCode == http.StatusOK && common.DecodeJson(http.MaxBytesReader(nil, profileResponse.Body, 1<<20), &profile) == nil && len(profile.Email) <= 320 && len(profile.ID) <= 256 {
			credential.AccountID, credential.AccountLabel = profile.ID, profile.Email
		}
		profileResponse.Body.Close()
	}
	return nil
}
