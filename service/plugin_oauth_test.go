package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPluginOAuthCryptoAndContract(t *testing.T) {
	for _, test := range []struct {
		origin, host string
		valid        bool
	}{
		{"https://new-api.appedia.web.id", "new-api.appedia.web.id", true},
		{"https://another.example", "another.example", true},
		{"http://localhost:4300", "localhost:4300", true},
		{"https://attacker.example", "gateway.example", false},
		{"https://gateway.example/path", "gateway.example", false},
		{"http://gateway.example", "gateway.example", false},
		{"https://user@gateway.example", "gateway.example", false},
	} {
		_, err := pluginoauth.BrowserOrigin(test.origin, test.host)
		require.Equal(t, test.valid, err == nil, test.origin)
	}
	require.NotEqual(t, pluginOAuthEnvPrefix("a-b"), pluginOAuthEnvPrefix("a_b"))
	t.Setenv("PLUGIN_OAUTH_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	raw, err := pluginoauth.Seal(pluginoauth.Credential{AccessToken: "access-secret", RefreshToken: "refresh-secret"}, "channel:1")
	require.NoError(t, err)
	require.NotContains(t, raw, "secret")
	var token pluginoauth.Credential
	require.NoError(t, pluginoauth.Open(raw, "channel:1", &token))
	require.Equal(t, "access-secret", token.AccessToken)
	require.Error(t, pluginoauth.Open(raw, "channel:2", &token))
	require.Error(t, pluginoauth.Open(raw[:len(raw)-5]+"AAAAA", "channel:1", &token))
	t.Setenv("PLUGIN_OAUTH_ENCRYPTION_KEY", "invalid")
	require.ErrorIs(t, pluginoauth.Open(raw, "channel:1", &token), pluginoauth.ErrEncryptionKey)

	config := pluginoauth.Config{AuthorizationURL: "https://provider.example/authorize", TokenURL: "https://provider.example/token", ClientID: "public-client", Scopes: []string{"inference"}}
	address, verifier, err := config.Authorize("state", "https://gateway.example/oauth/plugin-connect")
	require.NoError(t, err)
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	challenge := sha256.Sum256([]byte(verifier))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(challenge[:]), parsed.Query().Get("code_challenge"))
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	config.AuthorizationParams = map[string]string{"code_challenge_method": "plain"}
	require.Error(t, config.Validate())
	config.AuthorizationParams = nil
	config.TokenURL = "http://provider.example/token"
	require.Error(t, config.Validate())
	_, err = BindVerificationOperation(PluginOAuthOperation(1, "other"))
	require.Error(t, err)
}

type pluginOAuthProviderTransport func(*http.Request) (*http.Response, error)

func (transport pluginOAuthProviderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestPluginOAuthProviderPresets(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			config, err := pluginoauth.ProviderConfig(provider)
			require.NoError(t, err)
			address, verifier, err := config.Authorize("bound-state", config.RedirectURI)
			require.NoError(t, err)
			parsed, err := url.Parse(address)
			require.NoError(t, err)
			require.Equal(t, "bound-state", parsed.Query().Get("state"))
			require.Equal(t, config.RedirectURI, parsed.Query().Get("redirect_uri"))
			require.Len(t, verifier, 43)
			var calls []string
			client := &http.Client{Transport: pluginOAuthProviderTransport(func(request *http.Request) (*http.Response, error) {
				calls = append(calls, request.URL.String())
				body := `{"access_token":"native-access","refresh_token":"native-refresh","expires_in":3600}`
				switch request.URL.String() {
				case config.TokenURL:
					var values map[string]string
					if provider == "claude" {
						require.Equal(t, "application/json", request.Header.Get("Content-Type"))
						require.NoError(t, common.DecodeJson(request.Body, &values))
						require.Equal(t, "bound-state", values["state"])
					} else {
						require.NoError(t, request.ParseForm())
						values = map[string]string{"client_id": request.Form.Get("client_id"), "code_verifier": request.Form.Get("code_verifier")}
						if provider == "antigravity" {
							require.NotEmpty(t, request.Form.Get("client_secret"))
						}
					}
					require.Equal(t, config.ClientID, values["client_id"])
					require.Equal(t, verifier, values["code_verifier"])
				case "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
					require.Equal(t, "Bearer native-access", request.Header.Get("Authorization"))
					if provider == "antigravity" {
						require.Equal(t, pluginoauth.AntigravityUserAgent, request.Header.Get("User-Agent"))
					}
					body = `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`
				case "https://cloudcode-pa.googleapis.com/v1internal:onboardUser":
					var payload map[string]any
					require.NoError(t, common.DecodeJson(request.Body, &payload))
					require.Equal(t, "free-tier", payload["tierId"])
					body = `{"done":true,"response":{"cloudaicompanionProject":{"id":"provider-project"}}}`
				case "https://www.googleapis.com/oauth2/v1/userinfo?alt=json":
					body = `{"id":"google-account","email":"google-account@example.test"}`
				default:
					t.Fatalf("unexpected provider destination %s", request.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			credential, err := config.Exchange(t.Context(), client, url.Values{"grant_type": {"authorization_code"}, "code": {"native-code"}, "state": {"bound-state"}, "code_verifier": {verifier}}, "")
			require.NoError(t, err)
			if provider == "codex" {
				credential.AccessToken = "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"provider-account"}}`)) + ".signature"
			}
			require.NoError(t, pluginoauth.CompleteProvider(t.Context(), client, *config, credential))
			if provider == "codex" {
				require.Equal(t, "provider-account", credential.AccountID)
			}
			if provider == "antigravity" {
				require.Equal(t, "provider-project", credential.ProjectID)
				require.Equal(t, "google-account@example.test", credential.AccountLabel)
				require.Len(t, calls, 4)
			}
			config.TokenURL = "https://another.example/token"
			require.Error(t, config.Validate())
		})
	}
	_, err := pluginoauth.ProviderConfig("unknown")
	require.Error(t, err)
}

func TestPluginOAuthTokenExchangeBoundaries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(w, request, "/must-not-follow", http.StatusTemporaryRedirect)
		case "/invalid":
			fmt.Fprint(w, `{"access_token":"secret","expires_in":3600,"token_type":"Basic"}`)
		case "/error":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "access-secret refresh-secret")
		case "/oversize":
			fmt.Fprint(w, strings.Repeat("x", (1<<20)+1))
		case "/json":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var values map[string]string
			require.NoError(t, common.Unmarshal(body, &values))
			require.Equal(t, "public-client", values["client_id"])
			require.Equal(t, "application/json", request.Header.Get("Content-Type"))
			fmt.Fprint(w, `{"access_token":"access","token_type":"Bearer","expires_in":3600}`)
		}
	}))
	defer server.Close()
	config := pluginoauth.Config{AuthorizationURL: server.URL + "/authorize", ClientID: "public-client", Scopes: []string{}}
	for _, path := range []string{"redirect", "invalid", "error", "oversize"} {
		config.TokenURL = server.URL + "/" + path
		_, err := config.Exchange(context.Background(), server.Client(), url.Values{"grant_type": {"refresh_token"}}, "old-refresh")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
	require.EqualValues(t, 4, requests.Load(), "the redirect target must not be requested")
	config.TokenURL, config.TokenEncoding = server.URL+"/json", "json"
	credential, err := config.Exchange(context.Background(), server.Client(), url.Values{"grant_type": {"refresh_token"}}, "old-refresh")
	require.NoError(t, err)
	require.Equal(t, "old-refresh", credential.RefreshToken, "omitted refresh tokens must retain the previous one")
}

func setupPluginOAuthDB(t *testing.T, dialect string) AuthIdentity {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "mysql":
		dsn := os.Getenv("PLUGIN_OAUTH_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set PLUGIN_OAUTH_TEST_MYSQL_DSN to a dedicated test database")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("PLUGIN_OAUTH_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set PLUGIN_OAUTH_TEST_POSTGRES_DSN to a dedicated test database")
		}
		driver = postgres.Open(dsn)
	default:
		driver = sqlite.Open(t.TempDir() + "/oauth.db")
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previousDB, previousRegistry := model.DB, jsplugin.DefaultRegistry
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	previousType := common.MainDatabaseType()
	model.DB, jsplugin.DefaultRegistry = db, jsplugin.NewRegistry()
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if dialect == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	t.Cleanup(func() {
		model.DB, jsplugin.DefaultRegistry = previousDB, previousRegistry
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
		common.SetMainDatabaseType(previousType)
		_ = sqlDB.Close()
	})
	models := []any{&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuthFlow{}, &model.Channel{}, &model.Ability{}, &model.Option{}, &model.TwoFA{}, &model.PasskeyCredential{}}
	for _, table := range models {
		require.NoError(t, db.Migrator().DropTable(table))
	}
	for range 2 {
		require.NoError(t, db.AutoMigrate(models...))
	}
	password, err := common.Password2Hash("test-password")
	require.NoError(t, err)
	user := model.User{Username: "oauth-admin", Password: password, Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	session := model.UserSession{SID: "oauth-test-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive,
		RefreshHash: "test-refresh-hash", LoginMethod: "password", CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, db.Create(&session).Error)
	t.Setenv("PLUGIN_OAUTH_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	return AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
}

func pluginOAuthTestAuthorization(t *testing.T, identity AuthIdentity, id int, action string) *model.AuthFlowAuthorization {
	t.Helper()
	operation := PluginOAuthOperation(id, action)
	proof, err := VerifySecurityInput(identity, VerificationInput{Method: VerificationMethodPassword, Scope: operation.Scope, Context: operation.Context, Password: "test-password"})
	require.NoError(t, err)
	authorization, err := ConsumeOperationProof(proof.ProofToken, identity, operation)
	require.NoError(t, err)
	return authorization
}

func TestPluginOAuthLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			identity := setupPluginOAuthDB(t, dialect)
			var exchanges atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/token" {
					http.NotFound(w, request)
					return
				}
				require.NoError(t, request.ParseForm())
				require.Equal(t, "public-client", request.Form.Get("client_id"))
				exchanges.Add(1)
				if request.Form.Get("grant_type") == "authorization_code" {
					require.Equal(t, "one-time-code", request.Form.Get("code"))
					require.Len(t, request.Form.Get("code_verifier"), 43)
					fmt.Fprint(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600}`)
				} else {
					require.Equal(t, "refresh-1", request.Form.Get("refresh_token"))
					fmt.Fprint(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600}`)
				}
			}))
			defer server.Close()
			previousClient, previousSSRF := httpClient, *system_setting.GetFetchSetting()
			httpClient = server.Client()
			system_setting.GetFetchSetting().EnableSSRFProtection = false
			t.Cleanup(func() { httpClient = previousClient; *system_setting.GetFetchSetting() = previousSSRF })
			config := pluginoauth.Config{AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", ClientID: "public-client", Scopes: []string{"inference"}}
			data, err := common.Marshal(config)
			require.NoError(t, err)
			source := `export const meta = {apiVersion:1,key:"oauth-test",name:"OAuth Test",version:"1.0.0",author:{name:"Test"},models:["test-model"],fetchMode:"per_task",requiredCapabilities:["oauth2-pkce@1"],auth:{type:"oauth2_pkce",oauth:` + string(data) + `}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/inference",headers:{Authorization:ctx.authHeader}}}
export function parseSubmitResponse(){return {taskId:"1"}}
export function buildQueryRequest(){return {url:"https://provider.example/query"}}
export function parseTaskResult(){return {status:"SUCCESS"}}`
			_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
			require.NoError(t, err)
			setting := `{"task_plugin_key":"oauth-test"}`
			channel := model.Channel{Type: constant.ChannelTypeTaskPlugin, Key: pluginoauth.PendingKey, Name: "OAuth channel", BaseURL: &server.URL, Setting: &setting, Models: "test-model", Group: "default", Status: common.ChannelStatusEnabled}
			require.NoError(t, model.DB.Create(&channel).Error)
			authorization := pluginOAuthTestAuthorization(t, identity, channel.Id, "connect")
			start, err := StartPluginOAuth(identity, authorization, channel.Id, "https://gateway.example")
			require.NoError(t, err)
			require.Equal(t, "https://gateway.example/oauth/plugin-connect", start.RedirectURI)
			callback := start.RedirectURI + "?code=one-time-code&state=" + start.State
			_, err = FinishPluginOAuth(context.Background(), AuthIdentity{UserID: identity.UserID, SessionID: "another-session", UserAuthVersion: 1, SessionVersion: 1}, start.State, callback)
			require.Error(t, err)
			_, err = FinishPluginOAuth(context.Background(), identity, start.State, strings.Replace(callback, "gateway.example", "attacker.example", 1))
			require.Error(t, err)
			_, err = FinishPluginOAuth(context.Background(), identity, start.State, callback+"&state=other")
			require.Error(t, err)
			require.Zero(t, exchanges.Load())
			id, err := FinishPluginOAuth(context.Background(), identity, start.State, callback)
			require.NoError(t, err)
			require.Equal(t, channel.Id, id)
			_, err = FinishPluginOAuth(context.Background(), identity, start.State, callback)
			require.Error(t, err)
			require.EqualValues(t, 1, exchanges.Load())
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			require.NotContains(t, stored.Key, "access-1")
			require.NotContains(t, stored.Key, "refresh-1")
			access, err := ResolvePluginOAuthToken(context.Background(), channel.Id, "oauth-test")
			require.NoError(t, err)
			require.Equal(t, "access-1", access)
			var credential pluginoauth.Credential
			require.NoError(t, pluginoauth.Open(stored.Key, pluginOAuthBinding(channel.Id, "oauth-test"), &credential))
			credential.ExpiresAt = time.Now().Add(-time.Minute).Unix()
			expired, err := pluginoauth.Seal(credential, pluginOAuthBinding(channel.Id, "oauth-test"))
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&channel).Update("key", expired).Error)
			var wait sync.WaitGroup
			for range 2 {
				wait.Go(func() {
					access, err := ResolvePluginOAuthToken(context.Background(), channel.Id, "oauth-test")
					require.NoError(t, err)
					require.Equal(t, "access-2", access)
				})
			}
			wait.Wait()
			require.EqualValues(t, 2, exchanges.Load(), "concurrent requests must perform only one refresh")
			status, err := GetPluginOAuthStatus(channel.Id)
			require.NoError(t, err)
			require.True(t, status.Connected)
			pending, err := StartPluginOAuth(identity, pluginOAuthTestAuthorization(t, identity, channel.Id, "connect"), channel.Id, "https://other-gateway.example")
			require.NoError(t, err)
			require.Equal(t, "https://other-gateway.example/oauth/plugin-connect", pending.RedirectURI)
			accountB := model.Channel{Type: constant.ChannelTypeTaskPlugin, Key: pluginoauth.PendingKey, Name: "Second OAuth account", BaseURL: &server.URL, Setting: &setting, Models: "test-model", Group: "default", Status: common.ChannelStatusEnabled}
			require.NoError(t, model.DB.Create(&accountB).Error)
			secondCredential := pluginoauth.Credential{AccessToken: "account-b-access", RefreshToken: "account-b-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), ConfigHash: config.Hash(), AccountLabel: "account-b@example.test"}
			secondKey, err := pluginoauth.Seal(secondCredential, pluginOAuthBinding(accountB.Id, "oauth-test"))
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&accountB).Update("key", secondKey).Error)
			require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "test-model", ChannelId: channel.Id, Enabled: true}).Error)
			require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "test-model", ChannelId: accountB.Id, Enabled: true}).Error)
			eligible, err := model.EligibleChannels("default", "test-model", nil)
			require.NoError(t, err)
			require.Len(t, eligible, 2, "both independently connected accounts must be eligible")
			foreignGroup, err := model.EligibleChannels("another-group", "test-model", nil)
			require.NoError(t, err)
			require.Empty(t, foreignGroup, "account selection must not cross group permissions")
			require.NoError(t, DisconnectPluginOAuth(context.Background(), identity, pluginOAuthTestAuthorization(t, identity, channel.Id, "disconnect"), channel.Id))
			secondAccess, err := ResolvePluginOAuthToken(t.Context(), accountB.Id, "oauth-test")
			require.NoError(t, err)
			require.Equal(t, "account-b-access", secondAccess, "disconnecting one account must not disconnect another")
			secondStatus, err := GetPluginOAuthStatus(accountB.Id)
			require.NoError(t, err)
			require.Equal(t, "account-b@example.test", secondStatus.AccountLabel)
			_, err = ResolvePluginOAuthToken(context.Background(), channel.Id, "oauth-test")
			require.Error(t, err)
			_, err = FinishPluginOAuth(context.Background(), identity, pending.State, pending.RedirectURI+"?code=one-time-code&state="+pending.State)
			require.Error(t, err, "disconnect must invalidate older connection flows")
			require.EqualValues(t, 2, exchanges.Load())
			status, err = GetPluginOAuthStatus(channel.Id)
			require.NoError(t, err)
			require.False(t, status.Connected)
			expiring, err := StartPluginOAuth(identity, pluginOAuthTestAuthorization(t, identity, channel.Id, "connect"), channel.Id, "https://gateway.example")
			require.NoError(t, err)
			flow, err := model.GetAuthFlow(expiring.State, model.AuthFlowMatch{Purpose: pluginOAuthPurpose})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(flow).Update("expires_at", time.Now().Add(-time.Second)).Error)
			_, err = FinishPluginOAuth(context.Background(), identity, expiring.State, expiring.RedirectURI+"?code=one-time-code&state="+expiring.State)
			require.Error(t, err)
			require.EqualValues(t, 2, exchanges.Load())
			revoked, err := StartPluginOAuth(identity, pluginOAuthTestAuthorization(t, identity, channel.Id, "connect"), channel.Id, "https://gateway.example")
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", identity.SessionID).Update("version", 2).Error)
			_, err = FinishPluginOAuth(context.Background(), identity, revoked.State, revoked.RedirectURI+"?code=one-time-code&state="+revoked.State)
			require.Error(t, err)
			require.EqualValues(t, 2, exchanges.Load())
		})
	}
}

func TestPluginOAuthPoolPreservesWarmSessionsAndSplitsAgents(t *testing.T) {
	pool := oauthAccountPool{bindings: map[string]oauthPoolBinding{}, active: map[int]int{}, cooldown: map[int]time.Time{}}
	accounts := []*model.Channel{{Id: 1}, {Id: 2}}
	now := time.Now()
	accountA, hit, releaseA := pool.acquire("agent-a", accounts, 1, now)
	require.NotNil(t, accountA)
	require.False(t, hit)
	accountB, _, releaseB := pool.acquire("agent-b", accounts, 1, now)
	require.NotNil(t, accountB)
	require.NotEqual(t, accountA.Id, accountB.Id, "two concurrent agents must use separate account capacity")
	blocked, warm, _ := pool.acquire("agent-a", accounts, 1, now)
	require.Nil(t, blocked, "a busy warm conversation must wait rather than use a different account")
	require.True(t, warm)
	releaseA()
	releaseA()
	releaseB()
	continued, hit, release := pool.acquire("agent-a", accounts, 1, now.Add(time.Second))
	require.True(t, hit)
	require.Equal(t, accountA.Id, continued.Id, "a continuation must keep its warm account")
	release()
	pool.cooldown[accountA.Id] = now.Add(time.Minute)
	fallback, hit, release := pool.acquire("agent-a", accounts, 1, now.Add(2*time.Second))
	require.False(t, hit)
	require.Equal(t, accountB.Id, fallback.Id, "a rate-limited account must allow fallback before inference begins")
	release()
	continued, hit, release = pool.acquire("agent-a", accounts, 1, now.Add(3*time.Second))
	require.True(t, hit)
	require.Equal(t, fallback.Id, continued.Id, "a migrated session must stay on its new account")
	release()
	_, _, release = pool.acquire("isolated-tenant-agent-a", accounts, 1, now.Add(4*time.Second))
	require.NotNil(t, release)
	release()
	require.NotContains(t, pool.active, accountA.Id)
	require.Empty(t, pool.active, "all request reservations must be released")
}

func TestAksaraPersonalProfileDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			setupPluginOAuthDB(t, dialect)
			t.Setenv("AKSARA_PERSONAL_PROFILE", "true")
			previousPrices, _ := common.Marshal(billing_setting.GetPluginBillingExprCopy())
			previousRatios := ratio_setting.ModelRatio2JSONString()
			previousMap, previousRetries, previousEnabled := common.OptionMap, common.RetryTimes, constant.TaskPluginEnabled
			common.OptionMap = make(map[string]string)
			t.Cleanup(func() {
				require.NoError(t, model.UpdateOptionsBulk(map[string]string{billing_setting.PluginBillingExprOption: string(previousPrices), "ModelRatio": previousRatios}))
				common.OptionMap = previousMap
				common.RetryTimes = previousRetries
				constant.TaskPluginEnabled = previousEnabled
			})
			for _, key := range []string{"codex-oauth", "claude-oauth", "antigravity-oauth"} {
				source, err := plugins.Source(key)
				require.NoError(t, err)
				_, err = jsplugin.DefaultRegistry.RegisterFactory(source, jsplugin.Options{})
				require.NoError(t, err)
			}
			custom := `{"codex-oauth::gpt-6.1-sol":"tier(\"custom\", 0.01)"}`
			require.NoError(t, model.UpdateOptionsBulk(map[string]string{billing_setting.PluginBillingExprOption: custom, "ModelRatio": `{"gpt-6.1-sol":0.5}`}))
			require.NoError(t, BootstrapAksaraPersonalProfile())
			var channels []model.Channel
			require.NoError(t, model.DB.Find(&channels).Error)
			require.Len(t, channels, 6)
			for _, channel := range channels {
				require.True(t, pluginoauth.IsPendingKey(channel.Key))
				require.Equal(t, "default", channel.Group)
				require.NotEmpty(t, channel.GetBaseURL())
				require.NotEqual(t, constant.ChannelTypeTaskPlugin, channel.Type)
				require.Empty(t, channel.GetSetting().TaskPluginKey)
			}
			testPluginOAuthPoolRouting(t, channels)
			price, ok := billing_setting.GetPluginBillingExpr("codex-oauth", "gpt-6.1-sol")
			require.True(t, ok)
			require.Equal(t, `tier("custom", 0.01)`, price, "existing admin provider prices must be preserved")
			price, ok = billing_setting.GetPluginBillingExpr("claude-oauth", "claude-sonnet-5")
			require.True(t, ok)
			require.NoError(t, billing_setting.SmokeTestTaskExpr(price, map[string]jsplugin.UsageFieldSchema{}))
			ratio, configured, _ := ratio_setting.GetModelRatio("gpt-6.1-sol")
			require.True(t, configured)
			require.Equal(t, 0.5, ratio, "explicit model ratios must be preserved")
			ratio, configured, _ = ratio_setting.GetModelRatio("gpt-6.1-terra")
			require.True(t, configured)
			require.Zero(t, ratio, "native OAuth model ratios without an explicit price must default to zero")
			require.NoError(t, model.DB.Model(&channels[0]).Update("name", "User renamed account").Error)
			require.NoError(t, BootstrapAksaraPersonalProfile())
			var count int64
			require.NoError(t, model.DB.Model(&model.Channel{}).Count(&count).Error)
			require.EqualValues(t, 6, count, "restart must not recreate or overwrite user-edited channels")
		})
	}
}

func TestPluginOAuthLoopbackCallbackBinding(t *testing.T) {
	identity := setupPluginOAuthDB(t, "sqlite")
	payload, err := pluginoauth.Seal(pluginOAuthFlow{RedirectURI: "http://localhost:1455/auth/callback", BrowserOrigin: "https://gateway.example"}, "plugin-oauth-flow:"+identity.SessionID)
	require.NoError(t, err)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: pluginOAuthPurpose, UserId: identity.UserID, SessionId: identity.SessionID, Payload: payload, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	callback := "http://localhost:1455/auth/callback?code=one-time-code&state=" + state
	for _, address := range []string{callback, callback + "&code=duplicate", strings.Replace(callback, "localhost:1455", "attacker.example", 1), strings.Replace(callback, "/auth/callback", "/other", 1)} {
		response := httptest.NewRecorder()
		PluginOAuthLoopbackCallback(response, httptest.NewRequest(http.MethodGet, address, nil))
		if address != callback {
			require.Equal(t, 400, response.Code)
			continue
		}
		require.Equal(t, 303, response.Code)
		destination, err := url.Parse(response.Header().Get("Location"))
		require.NoError(t, err)
		require.Equal(t, "gateway.example", destination.Host)
		require.Equal(t, "/oauth/plugin-connect", destination.Path)
		require.Equal(t, state, destination.Query().Get("state"))
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	_, err = model.ConsumeAuthFlow(state, model.AuthFlowMatch{Purpose: pluginOAuthPurpose})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	PluginOAuthLoopbackCallback(response, httptest.NewRequest(http.MethodGet, callback, nil))
	require.Equal(t, 400, response.Code, "a consumed flow must not be forwarded")
}

func testPluginOAuthPoolRouting(t *testing.T, channels []model.Channel) {
	t.Helper()
	previousPool := pluginOAuthPool
	pluginOAuthPool = &oauthAccountPool{bindings: map[string]oauthPoolBinding{}, active: map[int]int{}, cooldown: map[int]time.Time{}}
	t.Cleanup(func() { pluginOAuthPool = previousPool })
	codex, exists := jsplugin.DefaultRegistry.Get("codex-oauth")
	require.True(t, exists)
	effective, err := pluginoauth.ProviderConfig("codex")
	require.NoError(t, err)
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeCodex {
			continue
		}
		credential, err := pluginoauth.Seal(pluginoauth.Credential{AccessToken: "fixture-only", AccountID: channel.Name, ExpiresAt: time.Now().Add(time.Hour).Unix(), ConfigHash: effective.Hash()}, pluginOAuthBinding(channel.Id, "codex-oauth"))
		require.NoError(t, err)
		require.NoError(t, model.DB.Model(&channel).Update("key", credential).Error)
	}
	contexts := make([]*gin.Context, 0, 2)
	for _, session := range []string{"agent-a", "agent-b"} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/codex-oauth/v1/responses", nil)
		ctx.Request.Header.Set("X-Agent-Session-Id", session)
		ctx.Set(jsplugin.ContextKeyPinnedRoute, jsplugin.PinnedRoute{Plugin: codex})
		contexts = append(contexts, ctx)
	}
	first, _, selectionError := SelectChannelForRequest(contexts[0], "gpt-6.1-sol", &RetryParam{Ctx: contexts[0], TokenGroup: "default", ModelName: "gpt-6.1-sol", Retry: common.GetPointer(0)})
	require.Nil(t, selectionError)
	second, _, selectionError := SelectChannelForRequest(contexts[1], "gpt-6.1-sol", &RetryParam{Ctx: contexts[1], TokenGroup: "default", ModelName: "gpt-6.1-sol", Retry: common.GetPointer(0)})
	require.Nil(t, selectionError)
	require.NotEqual(t, first.Id, second.Id, "native routing must split two agents across accounts")
	ReleasePluginOAuthPool(contexts[0])
	ReleasePluginOAuthPool(contexts[1])
	continued, _, selectionError := SelectChannelForRequest(contexts[0], "gpt-6.1-sol", &RetryParam{Ctx: contexts[0], TokenGroup: "default", ModelName: "gpt-6.1-sol", Retry: common.GetPointer(0)})
	require.Nil(t, selectionError)
	require.Equal(t, first.Id, continued.Id)
	ReleasePluginOAuthPool(contexts[0])
	MarkPluginOAuthPoolFailure(contexts[0], first.Id, http.StatusTooManyRequests)
	fallback, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: contexts[0], TokenGroup: "default", ModelName: "gpt-6.1-sol", Retry: common.GetPointer(1)})
	require.NoError(t, err)
	require.Equal(t, second.Id, fallback.Id, "the controller retry path must honor account cooldown and migrate the session")
	blocked, _, _ := pluginOAuthPool.acquire("competing-agent", []*model.Channel{fallback}, 1, time.Now())
	require.Nil(t, blocked, "retry must reserve the replacement account's concurrency slot")
	ReleasePluginOAuthPool(contexts[0])
}

func TestParseAntigravityModels(t *testing.T) {
	ids, err := parseAntigravityModels([]byte(`{"models":{"b-model":{},"a-model":{"displayName":"A"},"hidden":{"isInternal":true}}}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a-model", "b-model"}, ids)
}
