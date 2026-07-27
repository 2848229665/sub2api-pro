package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func resetGatewayForwardingSettingsCacheForTest(t *testing.T) {
	t.Helper()
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	t.Cleanup(func() {
		gatewayForwardingSF.Forget("gateway_forwarding")
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	})
}

func TestSettingService_GetClaudeOAuthSystemPromptInjectionSettings(t *testing.T) {
	t.Run("defaults to enabled with empty prompt", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, &config.Config{})

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.True(t, enabled)
		require.Empty(t, prompt)
		require.Empty(t, blocks)
	})

	t.Run("uses configured switch prompt and blocks", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		const customPrompt = "custom prompt\n\nkeep spacing"
		const customBlocks = `[{"type":"text","text":"custom block","cache_control":true}]`
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
			SettingKeyClaudeOAuthSystemPrompt:                customPrompt,
			SettingKeyClaudeOAuthSystemPromptBlocks:          customBlocks,
		}}, &config.Config{})

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.False(t, enabled)
		require.Equal(t, customPrompt, prompt)
		require.Equal(t, customBlocks, blocks)
	})
}

func TestSettingService_OpenAICodexPromptCacheOptimization(t *testing.T) {
	t.Run("uses config when database value is missing", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		cfg := &config.Config{}
		cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled = true
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, cfg)

		require.True(t, svc.IsOpenAICodexPromptCacheOptimizationEnabled(context.Background()))

		settings, err := svc.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.True(t, settings.OpenAICodexPromptCacheOptimizationEnabled)
	})

	t.Run("database value overrides config", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		cfg := &config.Config{}
		cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled = true
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			SettingKeyOpenAICodexPromptCacheOptimizationEnabled: "false",
		}}, cfg)

		require.False(t, svc.IsOpenAICodexPromptCacheOptimizationEnabled(context.Background()))
	})

	t.Run("update refreshes the request hot-path cache immediately", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		cfg := &config.Config{}
		cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled = true
		repo := &gatewayTTLSettingRepo{data: map[string]string{}}
		svc := NewSettingService(repo, cfg)

		settings, err := svc.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.True(t, svc.IsOpenAICodexPromptCacheOptimizationEnabled(context.Background()))

		settings.OpenAICodexPromptCacheOptimizationEnabled = false
		require.NoError(t, svc.UpdateSettings(context.Background(), settings))
		require.Equal(t, "false", repo.data[SettingKeyOpenAICodexPromptCacheOptimizationEnabled])
		require.False(t, svc.IsOpenAICodexPromptCacheOptimizationEnabled(context.Background()))
	})
}

func TestOpenAIGatewayPromptCacheOptimizationUsesRuntimeSetting(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled = true
	settingService := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyOpenAICodexPromptCacheOptimizationEnabled: "false",
	}}, cfg)
	gateway := &OpenAIGatewayService{
		cfg:            cfg,
		settingService: settingService,
	}

	require.False(t, gateway.openAICodexPromptCacheOptimizationEnabled(context.Background()))
}
