package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetPluginOAuthStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}
	status, err := service.GetPluginOAuthStatus(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Plugin OAuth status is unavailable. Check the server configuration."})
		return
	}
	common.ApiSuccess(c, status)
}

func StartPluginOAuth(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Plugin OAuth requires a browser sign-in."})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}
	origin, err := pluginoauth.BrowserOrigin(c.GetHeader("Origin"), c.Request.Host)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Plugin OAuth requires a matching browser origin."})
		return
	}
	proof := middleware.RequireSecurityProof(c, service.PluginOAuthOperation(id, "connect"))
	if proof == nil {
		return
	}
	start, err := service.StartPluginOAuth(identity, proof, id, origin)
	if err != nil {
		recordManageAudit(c, "channel.oauth_start_failed", map[string]any{"channel_id": id})
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Could not start plugin OAuth. Check the plugin endpoints, redirect URI, and server encryption key."})
		return
	}
	recordManageAudit(c, "channel.oauth_start", map[string]any{"channel_id": id})
	common.ApiSuccess(c, start)
}

func FinishPluginOAuth(c *gin.Context) {
	if _, err := pluginoauth.BrowserOrigin(c.GetHeader("Origin"), c.Request.Host); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Plugin OAuth requires a matching browser origin."})
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Plugin OAuth requires a browser sign-in."})
		return
	}
	var request struct {
		State       string `json:"state"`
		CallbackURL string `json:"callback_url"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 24<<10)
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid plugin OAuth callback."})
		return
	}
	id, err := service.FinishPluginOAuth(c.Request.Context(), identity, request.State, request.CallbackURL)
	if err != nil {
		recordManageAudit(c, "channel.oauth_connect_failed", nil)
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Plugin OAuth connection failed. Start connecting again."})
		return
	}
	recordManageAudit(c, "channel.oauth_connect", map[string]any{"channel_id": id})
	common.ApiSuccess(c, gin.H{"channel_id": id})
}

func DisconnectPluginOAuth(c *gin.Context) {
	if _, err := pluginoauth.BrowserOrigin(c.GetHeader("Origin"), c.Request.Host); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Plugin OAuth requires a matching browser origin."})
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Plugin OAuth requires a browser sign-in."})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}
	proof := middleware.RequireSecurityProof(c, service.PluginOAuthOperation(id, "disconnect"))
	if proof == nil {
		return
	}
	if err := service.DisconnectPluginOAuth(c.Request.Context(), identity, proof, id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Could not disconnect plugin OAuth."})
		return
	}
	recordManageAudit(c, "channel.oauth_disconnect", map[string]any{"channel_id": id})
	common.ApiSuccess(c, gin.H{"channel_id": id})
}
