package service

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"gorm.io/gorm"
)

// BootstrapAksaraPersonalProfile is opt-in and runs once. It never replaces
// existing channel credentials or administrator-defined provider prices.
func BootstrapAksaraPersonalProfile() error {
	if os.Getenv("AKSARA_PERSONAL_PROFILE") != "true" {
		return nil
	}
	const marker = "aksara_personal_profile_v1"
	var existing model.Option
	if err := model.DB.Where(&model.Option{Key: marker}).First(&existing).Error; err == nil && existing.Value == "true" {
		return nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	prices := billing_setting.GetPluginBillingExprCopy()
	if prices == nil {
		prices = make(map[string]string)
	}
	for _, key := range []string{"codex-oauth", "claude-oauth", "antigravity-oauth"} {
		plugin, exists := jsplugin.DefaultRegistry.Get(key)
		if !exists {
			return fmt.Errorf("personal profile plugin %s is unavailable", key)
		}
		for _, name := range plugin.Meta.Models {
			priceKey := billing_setting.PluginBillingExprKey(key, name)
			if _, configured := prices[priceKey]; !configured {
				prices[priceKey] = `tier("personal", 0)`
			}
		}
		for _, suffix := range []string{"A", "B"} {
			name := plugin.Meta.Name + " " + suffix
			var count int64
			if err := model.DB.Model(&model.Channel{}).Where("name = ? AND type = ?", name, constant.ChannelTypeTaskPlugin).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			settings, err := common.Marshal(map[string]string{"task_plugin_key": key})
			if err != nil {
				return err
			}
			setting := string(settings)
			baseURL := plugin.Meta.BaseURL
			weight := uint(100)
			channel := model.Channel{Name: name, Type: constant.ChannelTypeTaskPlugin, Key: pluginoauth.PendingKey, Status: common.ChannelStatusEnabled, Models: strings.Join(plugin.Meta.Models, ","), Group: "default", BaseURL: &baseURL, Setting: &setting, Weight: &weight}
			if err := model.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&channel).Error; err != nil {
					return err
				}
				return channel.AddAbilities(tx)
			}); err != nil {
				return err
			}
		}
	}
	encoded, err := common.Marshal(prices)
	if err != nil {
		return err
	}
	// Startup is single-threaded here. Persist the profile options together
	// before caches or request handlers start; existing expressions are kept.
	return model.UpdateOptionsBulk(map[string]string{billing_setting.PluginBillingExprOption: string(encoded), "TaskPluginEnabled": "true", "RetryTimes": "2", marker: "true"})
}
