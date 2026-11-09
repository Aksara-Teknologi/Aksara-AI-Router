package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
)

// FetchAntigravityChannelModels lists the model ids the connected Google
// account can actually call, so the channel never advertises ids that return 404.
func FetchAntigravityChannelModels(channel *model.Channel) ([]string, error) {
	if channel == nil || !pluginoauth.IsManagedKey(channel.Key) {
		return nil, fmt.Errorf("channel is not a connected Antigravity OAuth account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	token, err := ResolvePluginOAuthToken(ctx, channel.Id, "antigravity-oauth")
	if err != nil {
		return nil, err
	}
	routing, err := PluginOAuthRouting(channel.Id, "antigravity-oauth", token)
	if err != nil {
		return nil, err
	}
	client, err := GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	payload, err := common.Marshal(map[string]string{"project": routing["projectId"]})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(channel.GetBaseURL(), "/") + "/v1internal:fetchAvailableModels"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", pluginoauth.AntigravityUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchAvailableModels status code: %d", response.StatusCode)
	}
	return parseAntigravityModels(body)
}

func parseAntigravityModels(body []byte) ([]string, error) {
	var result struct {
		Models map[string]struct {
			IsInternal bool `json:"isInternal"`
		} `json:"models"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Models))
	for id, info := range result.Models {
		if !info.IsInternal && id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
