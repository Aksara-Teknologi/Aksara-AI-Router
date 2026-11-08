// Package pluginoauth implements public-client OAuth for task plugin channels.
package pluginoauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const PendingKey = "plugin-oauth:pending"
const keyPrefix = "plugin-oauth:v1:"

var ErrCredential = errors.New("Plugin OAuth credentials are unavailable. Connect the account again.")
var ErrConfiguration = errors.New("Plugin OAuth configuration is invalid.")
var ErrProviderProject = errors.New("Antigravity did not return a Code Assist project. Activate Antigravity for this Google account, then connect again.")
var ErrEncryptionKey = errors.New("Configure PLUGIN_OAUTH_ENCRYPTION_KEY with a base64-encoded 32-byte key before connecting accounts.")

type Config struct {
	Provider            string            `json:"provider,omitempty"`
	AuthorizationURL    string            `json:"authorizationUrl,omitempty"`
	TokenURL            string            `json:"tokenUrl,omitempty"`
	ClientID            string            `json:"clientId,omitempty"`
	Scopes              []string          `json:"scopes,omitempty"`
	RedirectURI         string            `json:"redirectUri,omitempty"`
	TokenEncoding       string            `json:"tokenEncoding,omitempty"`
	AuthorizationParams map[string]string `json:"authorizationParams,omitempty"`
}

type Credential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at"`
	ConfigHash   string `json:"config_hash"`
	AccountID    string `json:"account_id,omitempty"`
	AccountLabel string `json:"account_label,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
}

func (config Config) Validate() error {
	if config.Provider != "" && config.AuthorizationURL == "" && config.TokenURL == "" && config.ClientID == "" && config.Scopes == nil && config.RedirectURI == "" && config.TokenEncoding == "" && config.AuthorizationParams == nil {
		preset, err := ProviderConfig(config.Provider)
		if err != nil {
			return err
		}
		return preset.Validate()
	}
	if err := config.ValidateProvider(); err != nil {
		return err
	}
	if strings.TrimSpace(config.ClientID) == "" || len(config.ClientID) > 512 || config.Scopes == nil || len(config.Scopes) > 32 {
		return ErrConfiguration
	}
	for _, address := range []string{config.AuthorizationURL, config.TokenURL} {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(address) > 2048 {
			return ErrConfiguration
		}
	}
	for _, scope := range config.Scopes {
		if scope == "" || len(scope) > 256 || strings.ContainsAny(scope, " \t\r\n") {
			return ErrConfiguration
		}
	}
	if config.TokenEncoding != "" && config.TokenEncoding != "form" && config.TokenEncoding != "json" {
		return ErrConfiguration
	}
	if config.RedirectURI != "" {
		if err := ValidateRedirectURI(config.RedirectURI); err != nil {
			return err
		}
	}
	if len(config.AuthorizationParams) > 16 {
		return ErrConfiguration
	}
	for key, value := range config.AuthorizationParams {
		switch key {
		case "response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "client_secret":
			return ErrConfiguration
		}
		if key == "" || len(key) > 128 || len(value) > 1024 {
			return ErrConfiguration
		}
	}
	return nil
}

func ValidateRedirectURI(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(address) > 2048 {
		return ErrConfiguration
	}
	loopback := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return ErrConfiguration
	}
	return nil
}

func BrowserOrigin(origin, requestHost string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Host, requestHost) || parsed.Path != "" || ValidateRedirectURI(origin) != nil {
		return "", ErrConfiguration
	}
	return origin, nil
}

func (config Config) Hash() string {
	data, _ := common.Marshal(config)
	digest := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (config Config) Authorize(state, redirectURI string) (string, string, error) {
	if err := config.Validate(); err != nil {
		return "", "", err
	}
	if config.Provider != "" && config.AuthorizationURL == "" {
		preset, _ := ProviderConfig(config.Provider)
		return preset.Authorize(state, redirectURI)
	}
	if err := ValidateRedirectURI(redirectURI); err != nil {
		return "", "", err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	challenge := sha256.Sum256([]byte(verifier))
	address, _ := url.Parse(config.AuthorizationURL)
	query := address.Query()
	for key, value := range config.AuthorizationParams {
		query.Set(key, value)
	}
	query.Set("response_type", "code")
	query.Set("client_id", config.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", strings.Join(config.Scopes, " "))
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	address.RawQuery = query.Encode()
	return address.String(), verifier, nil
}

func encryptionCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("PLUGIN_OAUTH_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, ErrEncryptionKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrEncryptionKey
	}
	return cipher.NewGCM(block)
}

func Seal(value any, binding string) (string, error) {
	aead, err := encryptionCipher()
	if err != nil {
		return "", err
	}
	data, err := common.Marshal(value)
	if err != nil {
		return "", ErrCredential
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, data, []byte(binding))
	return keyPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Open(raw, binding string, value any) error {
	aead, err := encryptionCipher()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(raw, keyPrefix) {
		return ErrCredential
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, keyPrefix))
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return ErrCredential
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(binding))
	if err != nil || common.Unmarshal(plain, value) != nil {
		return ErrCredential
	}
	return nil
}

func IsManagedKey(raw string) bool {
	return IsPendingKey(raw) || strings.HasPrefix(raw, keyPrefix)
}

func IsPendingKey(raw string) bool {
	return raw == PendingKey || strings.HasPrefix(raw, PendingKey+":")
}

func NewPendingKey() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return PendingKey + ":" + base64.RawURLEncoding.EncodeToString(random), nil
}

// Exchange never follows a token endpoint redirect or returns a provider body in an error.
func (config Config) Exchange(ctx context.Context, client *http.Client, values url.Values, previousRefreshToken string) (*Credential, error) {
	if client == nil {
		return nil, ErrConfiguration
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.Provider != "" && config.TokenURL == "" {
		preset, _ := ProviderConfig(config.Provider)
		return preset.Exchange(ctx, client, values, previousRefreshToken)
	}
	values.Set("client_id", config.ClientID)
	config.PrepareTokenValues(values)
	var body io.Reader = strings.NewReader(values.Encode())
	contentType := "application/x-www-form-urlencoded"
	if config.TokenEncoding == "json" {
		object := make(map[string]string, len(values))
		for key := range values {
			object[key] = values.Get(key)
		}
		data, err := common.Marshal(object)
		if err != nil {
			return nil, ErrCredential
		}
		body = bytes.NewReader(data)
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL, body)
	if err != nil {
		return nil, ErrConfiguration
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	boundedClient := *client
	boundedClient.Timeout = 20 * time.Second
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := boundedClient.Do(request)
	if err != nil {
		return nil, errors.New("Plugin OAuth token exchange failed.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Plugin OAuth token exchange failed (HTTP %d).", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrCredential
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Account      struct {
			UUID         string `json:"uuid"`
			EmailAddress string `json:"email_address"`
		} `json:"account"`
	}
	if common.Unmarshal(data, &token) != nil || token.AccessToken == "" || len(token.AccessToken) > 65536 || len(token.RefreshToken) > 65536 || strings.ContainsAny(token.AccessToken, "\r\n") || token.ExpiresIn <= 0 || token.ExpiresIn > 366*24*60*60 || (!strings.EqualFold(token.TokenType, "Bearer") && !(config.Provider != "" && token.TokenType == "")) {
		return nil, ErrCredential
	}
	if token.RefreshToken == "" {
		token.RefreshToken = previousRefreshToken
	}
	return &Credential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt: time.Now().Unix() + token.ExpiresIn, ConfigHash: config.Hash(), AccountID: token.Account.UUID, AccountLabel: token.Account.EmailAddress}, nil
}
