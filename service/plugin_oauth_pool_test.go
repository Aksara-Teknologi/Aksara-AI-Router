package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthPoolMovesWarmSessionAfterRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := pluginOAuthPool
	pool.mu.Lock()
	pool.bindings, pool.active, pool.cooldown = map[string]oauthPoolBinding{}, map[int]int{}, map[int]time.Time{}
	pool.mu.Unlock()

	candidates := []*model.Channel{{Id: 6}, {Id: 9}}
	first, _, release := pool.acquire("session", candidates, 1, time.Now())
	require.Equal(t, 6, first.Id)
	release()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(OAuthPoolSessionKey, "session")
	MarkPluginOAuthPoolFailure(c, first.Id, http.StatusTooManyRequests)

	retried, hit, release := pool.acquire("session", candidates, 1, time.Now())
	require.NotNil(t, retried)
	defer release()
	assert.Equal(t, 9, retried.Id)
	assert.False(t, hit)
}
