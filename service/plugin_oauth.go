package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	"gorm.io/gorm"
)

const pluginOAuthPurpose = "plugin_oauth"

var ErrPluginOAuthFlow = errors.New("Plugin OAuth connection expired or changed. Start connecting again.")

type PluginOAuthStatus struct {
	Supported    bool   `json:"supported"`
	Connected    bool   `json:"connected"`
	Stored       bool   `json:"stored"`
	PluginKey    string `json:"plugin_key,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	AccountLabel string `json:"account_label,omitempty"`
}

type PluginOAuthStart struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	RedirectURI      string `json:"redirect_uri"`
	ExpiresAt        int64  `json:"expires_at"`
}

type pluginOAuthFlow struct {
	ChannelID       int                          `json:"channel_id"`
	PluginKey       string                       `json:"plugin_key"`
	ConfigHash      string                       `json:"config_hash"`
	PreviousKeyHash string                       `json:"previous_key_hash"`
	Verifier        string                       `json:"verifier"`
	RedirectURI     string                       `json:"redirect_uri"`
	BrowserOrigin   string                       `json:"browser_origin,omitempty"`
	Authorization   *model.AuthFlowAuthorization `json:"authorization"`
}

func PluginOAuthOperation(channelID int, action string) VerificationOperation {
	data, _ := common.Marshal(ChannelOAuthContext{ChannelID: channelID, Action: action})
	return VerificationOperation{Scope: VerificationScopeChannelOAuthManage, Context: data}
}

func pluginOAuthBinding(channelID int, pluginKey string) string {
	return "plugin-oauth:" + strconv.Itoa(channelID) + ":" + pluginKey
}

func pluginOAuthKeyHash(key string) string {
	digest := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func pluginOAuthConfig(channel *model.Channel) (*pluginoauth.Config, string, error) {
	if channel.ChannelInfo.IsMultiKey {
		return nil, "", pluginoauth.ErrConfiguration
	}
	pluginKey := pluginOAuthChannelKey(channel)
	if pluginKey == "" {
		return nil, "", pluginoauth.ErrConfiguration
	}
	plugin, ok := jsplugin.DefaultRegistry.Get(pluginKey)
	if !ok || plugin.Meta.Auth.Type != "oauth2_pkce" || plugin.Meta.Auth.OAuth == nil {
		return nil, "", pluginoauth.ErrConfiguration
	}
	effective := *plugin.Meta.Auth.OAuth
	if effective.Provider != "" {
		preset, err := pluginoauth.ProviderConfig(effective.Provider)
		if err != nil {
			return nil, "", err
		}
		effective = *preset
	}
	if clientID := os.Getenv(pluginOAuthEnvPrefix(pluginKey) + "_CLIENT_ID"); clientID != "" {
		effective.ClientID = clientID
	}
	config := &effective
	if err := config.Validate(); err != nil {
		return nil, "", err
	}
	for _, endpoint := range []string{config.AuthorizationURL, config.TokenURL} {
		if jsplugin.ValidateRequestURL(endpoint, channel.GetBaseURL(), plugin.Meta.AllowedHosts) != nil {
			return nil, "", pluginoauth.ErrConfiguration
		}
	}
	return config, pluginKey, nil
}

func pluginOAuthChannelKey(channel *model.Channel) string {
	if channel == nil || channel.ChannelInfo.IsMultiKey {
		return ""
	}
	switch channel.Type {
	case constant.ChannelTypeTaskPlugin:
		return channel.GetSetting().TaskPluginKey
	case constant.ChannelTypeCodex:
		return "codex-oauth"
	case constant.ChannelTypeAnthropic:
		return "claude-oauth"
	case constant.ChannelTypeGemini:
		return "antigravity-oauth"
	default:
		return ""
	}
}

func GetPluginOAuthStatus(channelID int) (*PluginOAuthStatus, error) {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	config, pluginKey, err := pluginOAuthConfig(channel)
	if err != nil {
		return &PluginOAuthStatus{Supported: false, Stored: pluginoauth.IsManagedKey(channel.Key) && !pluginoauth.IsPendingKey(channel.Key)}, nil
	}
	status := &PluginOAuthStatus{Supported: true, PluginKey: pluginKey}
	if pluginoauth.IsPendingKey(channel.Key) || channel.Key == "" {
		return status, nil
	}
	var credential pluginoauth.Credential
	status.Stored = pluginoauth.IsManagedKey(channel.Key)
	if err := pluginoauth.Open(channel.Key, pluginOAuthBinding(channelID, pluginKey), &credential); err != nil {
		return status, nil
	}
	status.Connected = credential.ConfigHash == config.Hash() && (credential.ExpiresAt > time.Now().Unix() || credential.RefreshToken != "")
	status.ExpiresAt = credential.ExpiresAt
	status.AccountLabel = credential.AccountLabel
	if status.AccountLabel == "" {
		status.AccountLabel = credential.AccountID
	}
	return status, nil
}

func StartPluginOAuth(identity AuthIdentity, authorization *model.AuthFlowAuthorization, channelID int, origin string) (*PluginOAuthStart, error) {
	if err := ValidateFlowAuthorization(identity, PluginOAuthOperation(channelID, "connect"), authorization); err != nil {
		return nil, err
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	config, pluginKey, err := pluginOAuthConfig(channel)
	if err != nil {
		return nil, err
	}
	redirectURI := config.RedirectURI
	if redirectURI == "" {
		redirectURI = strings.TrimRight(origin, "/") + "/oauth/plugin-connect"
	}
	address, verifier, err := config.Authorize("", redirectURI)
	if err != nil {
		return nil, err
	}
	flow := pluginOAuthFlow{ChannelID: channelID, PluginKey: pluginKey, ConfigHash: config.Hash(), BrowserOrigin: origin,
		PreviousKeyHash: pluginOAuthKeyHash(channel.Key), Verifier: verifier, RedirectURI: redirectURI, Authorization: authorization}
	payload, err := pluginoauth.Seal(flow, "plugin-oauth-flow:"+identity.SessionID)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(10 * time.Minute)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: pluginOAuthPurpose,
		UserId: identity.UserID, SessionId: identity.SessionID, Payload: payload, ExpiresAt: expiresAt})
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(address)
	query := parsed.Query()
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	return &PluginOAuthStart{AuthorizationURL: parsed.String(), State: state, RedirectURI: redirectURI, ExpiresAt: expiresAt.Unix()}, nil
}

func pluginOAuthHTTPClient(config *pluginoauth.Config) (*http.Client, error) {
	if err := ValidateSSRFProtectedFetchURL(config.TokenURL); err != nil {
		return nil, pluginoauth.ErrConfiguration
	}
	return GetSSRFProtectedHTTPClient(), nil
}

func pluginOAuthEnvPrefix(pluginKey string) string {
	encoded := strings.NewReplacer("_", "_U_", "-", "_H_").Replace(pluginKey)
	return "PLUGIN_OAUTH_" + strings.ToUpper(encoded)
}

func pluginOAuthClientValues(pluginKey string, values url.Values) url.Values {
	if secret := os.Getenv(pluginOAuthEnvPrefix(pluginKey) + "_CLIENT_SECRET"); secret != "" {
		values.Set("client_secret", secret)
	}
	return values
}

func FinishPluginOAuth(ctx context.Context, identity AuthIdentity, state, callbackURL string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if len(state) != 43 || len(callbackURL) > 16384 {
		return 0, ErrPluginOAuthFlow
	}
	match := model.AuthFlowMatch{Purpose: pluginOAuthPurpose, UserId: identity.UserID, SessionId: identity.SessionID}
	stored, err := model.GetAuthFlow(state, match)
	if err != nil {
		return 0, ErrPluginOAuthFlow
	}
	var flow pluginOAuthFlow
	if pluginoauth.Open(stored.Payload, "plugin-oauth-flow:"+identity.SessionID, &flow) != nil {
		return 0, ErrPluginOAuthFlow
	}
	if err := ValidateFlowAuthorization(identity, PluginOAuthOperation(flow.ChannelID, "connect"), flow.Authorization); err != nil {
		return 0, err
	}
	if code, codeState, found := strings.Cut(callbackURL, "#"); found && !strings.Contains(callbackURL, "://") && codeState == state && code != "" {
		address, _ := url.Parse(flow.RedirectURI)
		address.RawQuery = url.Values{"code": {code}, "state": {state}}.Encode()
		callbackURL = address.String()
	}
	callback, err := url.Parse(callbackURL)
	if err != nil {
		return 0, ErrPluginOAuthFlow
	}
	expected, _ := url.Parse(flow.RedirectURI)
	query, err := url.ParseQuery(callback.RawQuery)
	if err != nil || callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || callback.User != nil || callback.Fragment != "" || len(query["state"]) != 1 || query.Get("state") != state || len(query["code"]) != 1 || query.Get("code") == "" || query.Get("error") != "" {
		return 0, ErrPluginOAuthFlow
	}
	channel, err := model.GetChannelById(flow.ChannelID, true)
	if err != nil {
		return 0, ErrPluginOAuthFlow
	}
	config, pluginKey, err := pluginOAuthConfig(channel)
	if err != nil || pluginKey != flow.PluginKey || config.Hash() != flow.ConfigHash || pluginOAuthKeyHash(channel.Key) != flow.PreviousKeyHash {
		return 0, ErrPluginOAuthFlow
	}
	client, err := pluginOAuthHTTPClient(config)
	if err != nil {
		return 0, err
	}
	// Consume before contacting the provider: simultaneous callbacks cannot exchange the same code.
	if _, err := model.ConsumeAuthFlowWithAction(state, match, func(tx *gorm.DB, _ *model.AuthFlow) error {
		return model.ValidateAuthSessionWithTx(tx, identity)
	}); err != nil {
		return 0, ErrPluginOAuthFlow
	}
	credential, err := config.Exchange(ctx, client, pluginOAuthClientValues(pluginKey, url.Values{"grant_type": {"authorization_code"},
		"code": {query.Get("code")}, "state": {state}, "code_verifier": {flow.Verifier}, "redirect_uri": {flow.RedirectURI}}), "")
	if err != nil {
		return 0, err
	}
	if err := pluginoauth.CompleteProvider(ctx, client, *config, credential); err != nil {
		return 0, err
	}
	key, err := pluginoauth.Seal(credential, pluginOAuthBinding(flow.ChannelID, pluginKey))
	if err != nil {
		return 0, err
	}
	if err := model.UpdateChannelCredential(ctx, flow.ChannelID, &identity, func(current *model.Channel) (string, error) {
		currentConfig, currentPlugin, err := pluginOAuthConfig(current)
		if err != nil || currentPlugin != pluginKey || currentConfig.Hash() != flow.ConfigHash || pluginOAuthKeyHash(current.Key) != flow.PreviousKeyHash {
			return "", ErrPluginOAuthFlow
		}
		return key, nil
	}); err != nil {
		return 0, err
	}
	model.InitChannelCache()
	return flow.ChannelID, nil
}

func DisconnectPluginOAuth(ctx context.Context, identity AuthIdentity, authorization *model.AuthFlowAuthorization, channelID int) error {
	if err := ValidateFlowAuthorization(identity, PluginOAuthOperation(channelID, "disconnect"), authorization); err != nil {
		return err
	}
	if err := model.UpdateChannelCredential(ctx, channelID, &identity, func(channel *model.Channel) (string, error) {
		if _, _, err := pluginOAuthConfig(channel); err != nil || !pluginoauth.IsManagedKey(channel.Key) {
			return "", pluginoauth.ErrCredential
		}
		return pluginoauth.NewPendingKey()
	}); err != nil {
		return err
	}
	model.InitChannelCache()
	return nil
}

// ResolvePluginOAuthToken reads authoritative credentials so disconnect and token
// rotation take effect even when a relay request carries an older channel cache.
func ResolvePluginOAuthToken(ctx context.Context, channelID int, expectedPluginKey string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return "", pluginoauth.ErrCredential
	}
	config, pluginKey, err := pluginOAuthConfig(channel)
	if err != nil || pluginKey != expectedPluginKey {
		return "", pluginoauth.ErrCredential
	}
	var credential pluginoauth.Credential
	if err := pluginoauth.Open(channel.Key, pluginOAuthBinding(channelID, pluginKey), &credential); err != nil || credential.ConfigHash != config.Hash() {
		return "", pluginoauth.ErrCredential
	}
	if credential.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		return credential.AccessToken, nil
	}
	var accessToken string
	err = model.UpdateChannelCredential(ctx, channelID, nil, func(current *model.Channel) (string, error) {
		currentConfig, currentPlugin, err := pluginOAuthConfig(current)
		if err != nil || currentPlugin != pluginKey || currentConfig.Hash() != config.Hash() {
			return "", pluginoauth.ErrCredential
		}
		if pluginoauth.Open(current.Key, pluginOAuthBinding(channelID, pluginKey), &credential) != nil || credential.ConfigHash != config.Hash() {
			return "", pluginoauth.ErrCredential
		}
		if credential.ExpiresAt > time.Now().Add(time.Minute).Unix() {
			accessToken = credential.AccessToken
			return current.Key, nil
		}
		if credential.RefreshToken == "" {
			return "", pluginoauth.ErrCredential
		}
		client, err := pluginOAuthHTTPClient(config)
		if err != nil {
			return "", err
		}
		rotated, err := config.Exchange(ctx, client, pluginOAuthClientValues(pluginKey, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken}}), credential.RefreshToken)
		if err != nil {
			return "", err
		}
		if rotated.AccountID == "" {
			rotated.AccountID = credential.AccountID
		}
		rotated.ProjectID = credential.ProjectID
		if rotated.AccountLabel == "" {
			rotated.AccountLabel = credential.AccountLabel
		}
		if config.Provider == "codex" {
			if err := pluginoauth.CompleteProvider(ctx, client, *config, rotated); err != nil {
				return "", err
			}
		}
		accessToken = rotated.AccessToken
		return pluginoauth.Seal(rotated, pluginOAuthBinding(channelID, pluginKey))
	})
	return accessToken, err
}

// ResolveNativeChannelOAuthToken is used by the standard relay adaptors. It
// reports managed/pending channel credentials separately so API-key channels
// continue through their existing authentication path.
func ResolveNativeChannelOAuthToken(ctx context.Context, channelID int, pluginKey string) (string, bool, error) {
	if channelID <= 0 {
		return "", false, nil
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return "", false, err
	}
	if !pluginoauth.IsManagedKey(channel.Key) && !pluginoauth.IsPendingKey(channel.Key) {
		return "", false, nil
	}
	token, err := ResolvePluginOAuthToken(ctx, channelID, pluginKey)
	return token, true, err
}

// PluginOAuthRouting exposes only routing identifiers, never refresh tokens.
func PluginOAuthRouting(channelID int, pluginKey, accessToken string) (map[string]string, error) {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, pluginoauth.ErrCredential
	}
	var credential pluginoauth.Credential
	if pluginoauth.Open(channel.Key, pluginOAuthBinding(channelID, pluginKey), &credential) != nil || credential.AccessToken != accessToken {
		return nil, pluginoauth.ErrCredential
	}
	return map[string]string{"accountId": credential.AccountID, "projectId": credential.ProjectID}, nil
}
