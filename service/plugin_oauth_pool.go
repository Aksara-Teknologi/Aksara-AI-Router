package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const oauthPoolReleaseKey = "plugin_oauth_pool_release"
const OAuthPoolSessionKey = "plugin_oauth_pool_session"

type oauthPoolBinding struct {
	ChannelID int
	ExpiresAt time.Time
}
type oauthAccountPool struct {
	mu       sync.Mutex
	bindings map[string]oauthPoolBinding
	active   map[int]int
	cooldown map[int]time.Time
}

// One gateway process owns the Android personal-server pool. No credentials or
// conversation text are stored here; only hashed session keys and channel IDs.
var pluginOAuthPool = &oauthAccountPool{bindings: map[string]oauthPoolBinding{}, active: map[int]int{}, cooldown: map[int]time.Time{}}

func (pool *oauthAccountPool) acquire(session string, candidates []*model.Channel, limit int, now time.Time) (*model.Channel, bool, func()) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	counts := make(map[int]int)
	for key, binding := range pool.bindings {
		if !binding.ExpiresAt.After(now) {
			delete(pool.bindings, key)
			continue
		}
		counts[binding.ChannelID]++
	}
	for id, until := range pool.cooldown {
		if !until.After(now) {
			delete(pool.cooldown, id)
		}
	}
	var chosen *model.Channel
	hit := false
	if binding, ok := pool.bindings[session]; ok && session != "" {
		for _, candidate := range candidates {
			if candidate.Id == binding.ChannelID && !pool.cooldown[candidate.Id].After(now) {
				// Keep a warm session on its account. A full account waits; it
				// does not silently move that conversation to a cold account.
				if pool.active[candidate.Id] >= limit {
					return nil, true, nil
				}
				chosen, hit = candidate, true
				break
			}
		}
	}
	if chosen == nil {
		for _, candidate := range candidates {
			if pool.active[candidate.Id] >= limit || pool.cooldown[candidate.Id].After(now) {
				continue
			}
			if chosen == nil {
				chosen = candidate
				continue
			}
			candidateLoad := float64(counts[candidate.Id]) / float64(max(1, candidate.GetWeight()))
			chosenLoad := float64(counts[chosen.Id]) / float64(max(1, chosen.GetWeight()))
			if candidate.GetPriority() > chosen.GetPriority() || candidate.GetPriority() == chosen.GetPriority() &&
				(pool.active[candidate.Id] < pool.active[chosen.Id] || pool.active[candidate.Id] == pool.active[chosen.Id] &&
					(candidateLoad < chosenLoad || candidateLoad == chosenLoad && candidate.Id < chosen.Id)) {
				chosen = candidate
			}
		}
	}
	if chosen == nil {
		return nil, false, nil
	}
	if session != "" {
		if _, exists := pool.bindings[session]; !exists && len(pool.bindings) >= 10000 {
			return nil, false, nil
		}
		pool.bindings[session] = oauthPoolBinding{ChannelID: chosen.Id, ExpiresAt: now.Add(time.Hour)}
	}
	pool.active[chosen.Id]++
	var once sync.Once
	release := func() {
		once.Do(func() {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			pool.active[chosen.Id]--
			if pool.active[chosen.Id] == 0 {
				delete(pool.active, chosen.Id)
			}
		})
	}
	return chosen, hit, release
}

func ReleasePluginOAuthPool(c *gin.Context) {
	if value, exists := c.Get(oauthPoolReleaseKey); exists {
		if release, ok := value.(func()); ok {
			release()
		}
		c.Set(oauthPoolReleaseKey, nil)
	}
}

func MarkPluginOAuthPoolFailure(c *gin.Context, channelID, status int) {
	if _, exists := c.Get(OAuthPoolSessionKey); !exists {
		return
	}
	if status != http.StatusTooManyRequests && status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusServiceUnavailable {
		return
	}
	pluginOAuthPool.mu.Lock()
	defer pluginOAuthPool.mu.Unlock()
	pluginOAuthPool.cooldown[channelID] = time.Now().Add(time.Minute)
	// Existing bindings survive cooldown: only a session that needs a
	// fallback moves, avoiding a stampede of otherwise idle warm sessions.
}

func selectPluginOAuthPoolChannel(c *gin.Context, modelName string, retry *RetryParam) (*model.Channel, string, *ChannelSelectError, bool) {
	if strings.EqualFold(os.Getenv("PLUGIN_OAUTH_POOL_ENABLED"), "false") {
		return nil, "", nil, false
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedRoute)
	pinned, ok := value.(jsplugin.PinnedRoute)
	if !exists || !ok || pinned.Plugin == nil || pinned.Plugin.Meta.Auth.OAuth == nil || pinned.Plugin.Meta.Auth.OAuth.Provider == "" {
		return nil, "", nil, false
	}
	ReleasePluginOAuthPool(c)
	groups := []string{retry.TokenGroup}
	if retry.TokenGroup == "auto" {
		groups = GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	var candidates []*model.Channel
	pin, hasPin, _ := GetChannelConstraints(c).ResolvedPin()
	usingGroup := retry.TokenGroup
	for _, group := range groups {
		eligible, err := model.EligibleChannels(group, modelName, GetChannelConstraints(c).Filters)
		if err != nil {
			return nil, group, &ChannelSelectError{StatusCode: 503, Message: "OAuth account pool is unavailable"}, true
		}
		for _, candidate := range eligible {
			if hasPin && candidate.Id != pin.ChannelId {
				continue
			}
			if candidate.Type == constant.ChannelTypeTaskPlugin && candidate.GetSetting().TaskPluginKey == pinned.Plugin.Meta.Key && pluginoauth.IsManagedKey(candidate.Key) && !pluginoauth.IsPendingKey(candidate.Key) {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) > 0 {
			usingGroup = group
			break
		}
	}
	var agentSession string
	for _, header := range []string{"X-Agent-Session-Id", "Session_id", "Thread_id", "Session-Id", "Thread-Id"} {
		if agentSession = strings.TrimSpace(c.GetHeader(header)); agentSession != "" {
			break
		}
	}
	if agentSession == "" {
		for _, path := range []string{"prompt_cache_key", "metadata.user_id", "sessionId"} {
			agentSession = extractChannelAffinityValue(c, operation_setting.ChannelAffinityKeySource{Type: "gjson", Path: path})
			if agentSession != "" {
				break
			}
		}
	}
	if len(agentSession) > 4096 {
		return nil, usingGroup, &ChannelSelectError{StatusCode: 400, Message: "Agent session ID is too long"}, true
	}
	sessionKey := ""
	if agentSession != "" {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s:%s:%s", c.GetInt("id"), common.GetContextKeyInt(c, constant.ContextKeyTokenId), pinned.Plugin.Meta.Key, usingGroup, modelName, agentSession)))
		sessionKey = hex.EncodeToString(digest[:])
	}
	c.Set(OAuthPoolSessionKey, sessionKey)
	limit := 1
	if configured, err := strconv.Atoi(os.Getenv("PLUGIN_OAUTH_MAX_CONCURRENCY")); err == nil && configured >= 1 && configured <= 32 {
		limit = configured
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		chosen, hit, release := pluginOAuthPool.acquire(sessionKey, candidates, limit, time.Now())
		if chosen != nil {
			if retry.TokenGroup == "auto" {
				common.SetContextKey(c, constant.ContextKeyAutoGroup, usingGroup)
			}
			c.Set(oauthPoolReleaseKey, release)
			reason := "new_session_least_loaded"
			if hit {
				reason = "warm_session_account"
			}
			RequestPolicy(c).AddEvent(PolicyEvent{ChannelID: chosen.Id, Group: usingGroup, Rule: "OAuth account pool", Decision: PolicyDecision{Action: "select", Reason: reason, Source: "oauth_pool"}})
			return chosen, usingGroup, nil, true
		}
		if len(candidates) == 0 {
			return nil, usingGroup, &ChannelSelectError{StatusCode: 503, Message: "No connected OAuth account is available for this model and group"}, true
		}
		pluginOAuthPool.mu.Lock()
		healthy := false
		for _, candidate := range candidates {
			if !pluginOAuthPool.cooldown[candidate.Id].After(time.Now()) {
				healthy = true
				break
			}
		}
		pluginOAuthPool.mu.Unlock()
		if !healthy {
			c.Header("Retry-After", "60")
			return nil, usingGroup, &ChannelSelectError{StatusCode: 503, Message: "OAuth accounts are cooling down; retry later"}, true
		}
		if !time.Now().Before(deadline) {
			c.Header("Retry-After", "1")
			return nil, usingGroup, &ChannelSelectError{StatusCode: 429, Message: "OAuth accounts are busy; retry this agent session shortly"}, true
		}
		select {
		case <-c.Request.Context().Done():
			return nil, usingGroup, &ChannelSelectError{StatusCode: 408, Message: "Request cancelled while waiting for the OAuth account"}, true
		case <-time.After(100 * time.Millisecond):
		}
	}
}
