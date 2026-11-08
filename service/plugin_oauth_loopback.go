package service

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
)

// PluginOAuthLoopbackCallback forwards a native-client callback to the origin
// that authenticated and initiated the flow. It does not exchange the code or
// consume the state; the browser's authenticated finish request does both.
func PluginOAuthLoopbackCallback(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	query, err := url.ParseQuery(request.URL.RawQuery)
	if request.Method != http.MethodGet || err != nil || len(query["state"]) != 1 || len(query["code"]) != 1 || len(query.Get("state")) != 43 || query.Get("code") == "" || len(query.Get("code")) > 8192 || query.Get("error") != "" {
		http.Error(writer, "Invalid OAuth callback", http.StatusBadRequest)
		return
	}
	stored, err := model.GetAuthFlow(query.Get("state"), model.AuthFlowMatch{Purpose: pluginOAuthPurpose})
	if err != nil {
		http.Error(writer, "OAuth flow expired or unavailable", http.StatusBadRequest)
		return
	}
	var flow pluginOAuthFlow
	if pluginoauth.Open(stored.Payload, "plugin-oauth-flow:"+stored.SessionId, &flow) != nil {
		http.Error(writer, "OAuth flow unavailable", http.StatusBadRequest)
		return
	}
	expected, err := url.Parse(flow.RedirectURI)
	if err != nil || expected.Scheme != "http" || expected.Hostname() != "localhost" || !strings.EqualFold(request.Host, expected.Host) || request.URL.Path != expected.Path {
		http.Error(writer, "OAuth callback address mismatch", http.StatusBadRequest)
		return
	}
	allowed := expected.Host == "localhost:1455" && expected.Path == "/auth/callback" || (expected.Host == "localhost:54545" || expected.Host == "localhost:51121") && expected.Path == "/callback"
	origin, err := url.Parse(flow.BrowserOrigin)
	if !allowed || err != nil || origin == nil {
		http.Error(writer, "OAuth callback address mismatch", http.StatusBadRequest)
		return
	}
	if _, err := pluginoauth.BrowserOrigin(flow.BrowserOrigin, origin.Host); err != nil {
		http.Error(writer, "OAuth callback origin unavailable", http.StatusBadRequest)
		return
	}
	origin.Path = "/oauth/plugin-connect"
	origin.RawQuery = url.Values{"state": {query.Get("state")}, "code": {query.Get("code")}}.Encode()
	writer.Header().Set("Location", origin.String())
	writer.WriteHeader(http.StatusSeeOther)
}

func StartPluginOAuthLoopbackCallbacks() func() {
	var servers []*http.Server
	if os.Getenv("PLUGIN_OAUTH_LOOPBACK_CALLBACKS") == "true" {
		for _, port := range []string{"1455", "54545", "51121"} {
			listener, err := net.Listen("tcp", "127.0.0.1:"+port)
			if err != nil {
				common.SysError("OAuth loopback callback port " + port + " unavailable; use manual callback or browser bridge")
				continue
			}
			server := &http.Server{Handler: http.HandlerFunc(PluginOAuthLoopbackCallback), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
			servers = append(servers, server)
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					common.SysError("OAuth loopback callback listener stopped")
				}
			}()
		}
	}
	return func() {
		for _, server := range servers {
			_ = server.Close()
		}
	}
}
