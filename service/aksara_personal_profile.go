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
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// BootstrapAksaraPersonalProfile is opt-in and runs once. It never replaces
// existing channel credentials or administrator-defined provider prices.
func BootstrapAksaraPersonalProfile() error {
	if os.Getenv("AKSARA_PERSONAL_PROFILE") != "true" {
		return nil
	}
	if err := migrateAksaraOAuthChannels(); err != nil {
		return err
	}
	const marker = "aksara_personal_profile_v1"
	var existing model.Option
	profileCreated := false
	if err := model.DB.Where(&model.Option{Key: marker}).First(&existing).Error; err == nil && existing.Value == "true" {
		profileCreated = true
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	prices := billing_setting.GetPluginBillingExprCopy()
	if prices == nil {
		prices = make(map[string]string)
	}
	modelRatios := ratio_setting.GetModelRatioCopy()
	if modelRatios == nil {
		modelRatios = make(map[string]float64)
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
			if !ratio_setting.HasConfiguredModelRatio(name) {
				if _, fixedPrice := ratio_setting.GetModelPrice(name, false); !fixedPrice {
					modelRatios[name] = 0
				}
			}
		}
		if profileCreated {
			continue
		}
		channelType := map[string]int{"codex-oauth": constant.ChannelTypeCodex, "claude-oauth": constant.ChannelTypeAnthropic, "antigravity-oauth": constant.ChannelTypeGemini}[key]
		for _, suffix := range []string{"A", "B"} {
			name := plugin.Meta.Name + " " + suffix
			var count int64
			if err := model.DB.Model(&model.Channel{}).Where("name = ? AND type = ?", name, channelType).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			baseURL := plugin.Meta.BaseURL
			weight := uint(100)
			channel := model.Channel{Name: name, Type: channelType, Key: pluginoauth.PendingKey, Status: common.ChannelStatusEnabled, Models: strings.Join(plugin.Meta.Models, ","), Group: "default", BaseURL: &baseURL, Weight: &weight}
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
	encodedModelRatios, err := common.Marshal(modelRatios)
	if err != nil {
		return err
	}
	// Startup is single-threaded here. Persist the profile options together
	// before caches or request handlers start; existing prices and ratios are kept.
	options := map[string]string{
		billing_setting.PluginBillingExprOption: string(encoded),
		"ModelRatio":                            string(encodedModelRatios),
		"TaskPluginEnabled":                     "true",
		"RetryTimes":                            "2",
	}
	if !profileCreated {
		options[marker] = "true"
	}
	return model.UpdateOptionsBulk(options)
}

func migrateAksaraOAuthChannels() error {
	for _, key := range []string{"codex-oauth", "claude-oauth", "antigravity-oauth"} {
		plugin, exists := jsplugin.DefaultRegistry.Get(key)
		if !exists {
			return fmt.Errorf("personal profile plugin %s is unavailable", key)
		}
		targetType := map[string]int{"codex-oauth": constant.ChannelTypeCodex, "claude-oauth": constant.ChannelTypeAnthropic, "antigravity-oauth": constant.ChannelTypeGemini}[key]
		var channels []model.Channel
		if err := model.DB.Where("type = ? AND name IN ?", constant.ChannelTypeTaskPlugin, []string{plugin.Meta.Name + " A", plugin.Meta.Name + " B"}).Find(&channels).Error; err != nil {
			return err
		}
		for i := range channels {
			baseURL := plugin.Meta.BaseURL
			if err := model.DB.Model(&channels[i]).Updates(map[string]any{"type": targetType, "base_url": baseURL}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
