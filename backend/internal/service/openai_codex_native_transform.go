package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAICodexUpstreamIdentityContextKey          = "openai_codex_upstream_identity"
	openAICodexNativeOptimizationAppliedContextKey = "openai_codex_native_optimization_applied"
	openAIOfficialThreadIDHeader                   = "thread-id"
	openAIOfficialClientRequestIDHeader            = "x-client-request-id"
	openAICodexParentThreadIDHeader                = "x-codex-parent-thread-id"
)

type openAICodexUpstreamIdentity struct {
	RelayIdentity   openAICodexRelayIdentity
	InstallationID  string
	SessionID       string
	ThreadID        string
	ClientRequestID string
	ParentThreadID  string
	WindowID        string
	TurnMetadata    string
	TurnState       string
}

type openAICodexIdentityBodyOptions struct {
	Compact bool
}

type openAICodexPromptCacheOptimizationResult struct {
	Body        []byte
	RequestView openAIRequestView
	CodexResult codexTransformResult
	Applied     bool
}

func (s *OpenAIGatewayService) openAICodexPromptCacheOptimizationEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		if s.settingService.settingRepo == nil {
			return s.cfg != nil && s.cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled
		}
		if ctx == nil {
			ctx = context.Background()
		}
		return s.settingService.IsOpenAICodexPromptCacheOptimizationEnabled(ctx)
	}
	return s.cfg != nil && s.cfg.Gateway.OpenAICodexPromptCacheOptimizationEnabled
}

// tryApplyOpenAICodexPromptCacheOptimization owns the complete optional entry
// point. Keeping eligibility, pending-patch application, and identity reset in
// this file limits the upstream forwarding integration to a single hook.
func (s *OpenAIGatewayService) tryApplyOpenAICodexPromptCacheOptimization(
	c *gin.Context,
	account *Account,
	body []byte,
	requestView openAIRequestView,
	bodyAlreadyDecoded bool,
	isOfficialCodexClient bool,
	compatMessagesBridge bool,
	isCompactRequest bool,
) (openAICodexPromptCacheOptimizationResult, error) {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if !s.openAICodexPromptCacheOptimizationEnabled(ctx) {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}
	if !isOfficialCodexClient ||
		compatMessagesBridge ||
		isCompactRequest ||
		bodyAlreadyDecoded ||
		requestView.patchesDisabled {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}

	workingBody := body
	if requestView.HasPatches() {
		patchedBody, err := requestView.ApplyPatches()
		if err != nil {
			return openAICodexPromptCacheOptimizationResult{}, err
		}
		workingBody = patchedBody
	}

	transformedBody, codexResult, handled, err := tryTransformNativeCodexOAuthRequest(c, account, workingBody)
	if err != nil {
		return openAICodexPromptCacheOptimizationResult{}, err
	}
	if !handled {
		return openAICodexPromptCacheOptimizationResult{}, nil
	}
	return openAICodexPromptCacheOptimizationResult{
		Body:        transformedBody,
		RequestView: newOpenAIRequestView(transformedBody),
		CodexResult: codexResult,
		Applied:     true,
	}, nil
}

// tryTransformNativeCodexOAuthRequest keeps an official Codex Responses payload
// on a raw-JSON patch path. Native Codex already emits the schema accepted by
// ChatGPT's internal Responses endpoint, so decoding and re-marshalling stable
// instructions, tools, input, and text only creates unnecessary prompt churn.
//
// handled is false when the request needs one of the compatibility conversions
// owned by applyCodexOAuthTransformWithOptions.
func tryTransformNativeCodexOAuthRequest(
	c *gin.Context,
	account *Account,
	body []byte,
) (transformed []byte, result codexTransformResult, handled bool, err error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return body, codexTransformResult{}, false, nil
	}
	root := parseRawJSONView(body)
	if !canPatchNativeCodexOAuthRequest(root) {
		return body, codexTransformResult{}, false, nil
	}

	result.NormalizedModel = strings.TrimSpace(root.Get("model").String())
	rawPromptCacheKey := strings.TrimSpace(root.Get("prompt_cache_key").String())
	if rawPromptCacheKey == "" && c != nil && c.Request != nil {
		rawPromptCacheKey = strings.TrimSpace(c.Request.Header.Get(openAIOfficialSessionIDHeader))
		if rawPromptCacheKey == "" {
			rawPromptCacheKey = strings.TrimSpace(c.Request.Header.Get("session_id"))
		}
	}
	if rawPromptCacheKey == "" {
		rawPromptCacheKey = strings.TrimSpace(root.Get("client_metadata.session_id").String())
	}
	result.PromptCacheKey = rawPromptCacheKey

	transformed = body
	if model := root.Get("model"); model.Type == gjson.String && model.String() != result.NormalizedModel {
		transformed, err = sjson.SetBytes(transformed, "model", result.NormalizedModel)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex model: %w", err)
		}
	}

	if store := gjson.GetBytes(transformed, "store"); !store.Exists() || store.Type != gjson.False {
		transformed, err = sjson.SetBytes(transformed, "store", false)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex store: %w", err)
		}
	}
	if stream := gjson.GetBytes(transformed, "stream"); !stream.Exists() || stream.Type != gjson.True {
		transformed, err = sjson.SetBytes(transformed, "stream", true)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("patch native Codex stream: %w", err)
		}
	}

	for _, field := range openAICodexOAuthUnsupportedFields {
		if !gjson.GetBytes(transformed, field).Exists() {
			continue
		}
		transformed, err = sjson.DeleteBytes(transformed, field)
		if err != nil {
			return body, codexTransformResult{}, false, fmt.Errorf("delete native Codex field %s: %w", field, err)
		}
	}

	transformed, err = patchNativeCodexReasoningInclude(transformed)
	if err != nil {
		return body, codexTransformResult{}, false, err
	}
	transformed, err = patchNativeCodexInput(transformed)
	if err != nil {
		return body, codexTransformResult{}, false, err
	}

	identity, hasIdentity := openAICodexUpstreamIdentityFromContext(c)
	if !hasIdentity {
		identity, err = resolveOpenAICodexUpstreamIdentity(c, account, transformed, false)
		if err != nil {
			return body, codexTransformResult{}, false, err
		}
		if identity != (openAICodexUpstreamIdentity{}) {
			transformed, err = patchOpenAICodexIdentityBody(
				transformed,
				identity,
				openAICodexIdentityBodyOptions{},
			)
			if err != nil {
				return body, codexTransformResult{}, false, err
			}
		}
	}
	if c != nil && identity != (openAICodexUpstreamIdentity{}) {
		c.Set(openAICodexUpstreamIdentityContextKey, identity)
	}

	result.Modified = !bytes.Equal(body, transformed)
	return transformed, result, true, nil
}

func canPatchNativeCodexOAuthRequest(root gjson.Result) bool {
	if !root.IsObject() {
		return false
	}
	model := root.Get("model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" ||
		isCodexSparkModel(strings.TrimSpace(model.String())) {
		return false
	}
	instructions := root.Get("instructions")
	if instructions.Type != gjson.String || strings.TrimSpace(instructions.String()) == "" {
		return false
	}
	if root.Get("functions").Exists() || root.Get("function_call").Exists() {
		return false
	}
	if clientMetadata := root.Get("client_metadata"); clientMetadata.Exists() && !clientMetadata.IsObject() {
		return false
	}

	tools := root.Get("tools")
	if !tools.IsArray() || len(tools.Array()) == 0 {
		return false
	}
	toolsCompatible := true
	tools.ForEach(func(_, tool gjson.Result) bool {
		if !tool.IsObject() || strings.TrimSpace(tool.Get("type").String()) != "function" {
			return true
		}
		if strings.TrimSpace(tool.Get("name").String()) == "" || tool.Get("function").Exists() {
			toolsCompatible = false
			return false
		}
		return true
	})
	if !toolsCompatible || nativeCodexToolChoiceNeedsNormalization(root) {
		return false
	}

	input := root.Get("input")
	if !input.IsArray() {
		return false
	}
	inputCompatible := true
	input.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		switch strings.TrimSpace(item.Get("role").String()) {
		case "system", "tool":
			inputCompatible = false
			return false
		}
		if strings.TrimSpace(item.Get("type").String()) != "message" {
			return true
		}
		content := item.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, part gjson.Result) bool {
			text := part.Get("text")
			if text.Exists() && text.Type != gjson.String {
				inputCompatible = false
				return false
			}
			return true
		})
		return inputCompatible
	})
	return inputCompatible
}

func nativeCodexToolChoiceNeedsNormalization(root gjson.Result) bool {
	choice := root.Get("tool_choice")
	if !choice.Exists() || !choice.IsObject() {
		return false
	}
	choiceType := strings.TrimSpace(choice.Get("type").String())
	if choiceType == "" {
		return false
	}
	if choiceType == "function" {
		name := strings.TrimSpace(choice.Get("name").String())
		if name == "" || choice.Get("function").Exists() {
			return true
		}
		return !nativeCodexToolsContain(root.Get("tools"), "function", name)
	}
	if nativeCodexToolsContain(root.Get("tools"), choiceType, "") {
		return false
	}
	additionalTools := root.Get("input.#(type==\"additional_tools\")#.tools")
	return !nativeCodexToolsContain(additionalTools, choiceType, "")
}

func nativeCodexToolsContain(tools gjson.Result, toolType, functionName string) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if strings.TrimSpace(tool.Get("type").String()) != toolType {
			return true
		}
		if functionName != "" && strings.TrimSpace(tool.Get("name").String()) != functionName {
			return true
		}
		found = true
		return false
	})
	return found
}

func patchNativeCodexReasoningInclude(body []byte) ([]byte, error) {
	reasoning := gjson.GetBytes(body, "reasoning")
	hasReasoningField := false
	if reasoning.IsObject() {
		reasoning.ForEach(func(_, _ gjson.Result) bool {
			hasReasoningField = true
			return false
		})
	}
	if !hasReasoningField {
		return body, nil
	}
	const encryptedContent = "reasoning.encrypted_content"
	include := gjson.GetBytes(body, "include")
	if !include.Exists() || include.Type == gjson.Null {
		next, err := sjson.SetBytes(body, "include", []string{encryptedContent})
		if err != nil {
			return body, fmt.Errorf("set native Codex reasoning include: %w", err)
		}
		return next, nil
	}
	if !include.IsArray() {
		return body, nil
	}
	for _, value := range include.Array() {
		if value.Type == gjson.String && value.String() == encryptedContent {
			return body, nil
		}
	}
	next, err := sjson.SetBytes(body, "include.-1", encryptedContent)
	if err != nil {
		return body, fmt.Errorf("append native Codex reasoning include: %w", err)
	}
	return next, nil
}

func patchNativeCodexInput(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}

	items := make([][]byte, 0, len(input.Array()))
	changed := false
	index := 0
	var patchErr error
	input.ForEach(func(_, item gjson.Result) bool {
		currentIndex := index
		index++
		itemBody := []byte(item.Raw)
		if !item.IsObject() {
			items = append(items, itemBody)
			return true
		}

		itemType := strings.TrimSpace(item.Get("type").String())
		switch itemType {
		case "reasoning":
			if item.Get("id").Exists() {
				itemBody, patchErr = sjson.DeleteBytes(itemBody, "id")
				if patchErr != nil {
					patchErr = fmt.Errorf("delete input.%d reasoning id: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			summary := item.Get("summary")
			if !summary.Exists() || summary.Type == gjson.Null {
				itemBody, patchErr = sjson.SetBytes(itemBody, "summary", []any{})
				if patchErr != nil {
					patchErr = fmt.Errorf("set input.%d reasoning summary: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			items = append(items, itemBody)
			return true
		case "item_reference":
			id := item.Get("id")
			if id.Type == gjson.String && strings.HasPrefix(id.String(), "call_") {
				itemBody, patchErr = sjson.SetBytes(itemBody, "id", normalizeCodexCallID(id.String()))
				if patchErr != nil {
					patchErr = fmt.Errorf("normalize input.%d item reference: %w", currentIndex, patchErr)
					return false
				}
				changed = true
			}
			items = append(items, itemBody)
			return true
		}

		if isCodexToolCallItemType(itemType) {
			callID := item.Get("call_id").String()
			if strings.TrimSpace(callID) == "" {
				callID = item.Get("id").String()
				if strings.TrimSpace(callID) != "" {
					itemBody, patchErr = sjson.SetBytes(itemBody, "call_id", callID)
					if patchErr != nil {
						patchErr = fmt.Errorf("set input.%d call_id: %w", currentIndex, patchErr)
						return false
					}
					changed = true
				}
			}
			if callID != "" {
				normalizedCallID := normalizeCodexCallID(callID)
				if normalizedCallID != callID {
					itemBody, patchErr = sjson.SetBytes(itemBody, "call_id", normalizedCallID)
					if patchErr != nil {
						patchErr = fmt.Errorf("normalize input.%d call_id: %w", currentIndex, patchErr)
						return false
					}
					changed = true
				}
			}
		} else if item.Get("call_id").Exists() {
			itemBody, patchErr = sjson.DeleteBytes(itemBody, "call_id")
			if patchErr != nil {
				patchErr = fmt.Errorf("delete input.%d call_id: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}

		if codexInputItemRequiresName(itemType) && strings.TrimSpace(item.Get("name").String()) == "" {
			name := strings.TrimSpace(item.Get("tool_name").String())
			if name == "" {
				name = strings.TrimSpace(item.Get("function.name").String())
			}
			if name == "" {
				name = "tool"
			}
			itemBody, patchErr = sjson.SetBytes(itemBody, "name", name)
			if patchErr != nil {
				patchErr = fmt.Errorf("set input.%d tool name: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}

		id := item.Get("id")
		if id.Type == gjson.String && shouldStripOpenAIResponsesInputItemID(itemType, id.String()) {
			itemBody, patchErr = sjson.DeleteBytes(itemBody, "id")
			if patchErr != nil {
				patchErr = fmt.Errorf("delete input.%d invalid id: %w", currentIndex, patchErr)
				return false
			}
			changed = true
		}
		items = append(items, itemBody)
		return true
	})
	if patchErr != nil {
		return body, patchErr
	}
	if !changed {
		return body, nil
	}

	rebuilt := make([]byte, 0, len(input.Raw))
	rebuilt = append(rebuilt, '[')
	for i, item := range items {
		if i > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, item...)
	}
	rebuilt = append(rebuilt, ']')
	next, err := sjson.SetRawBytes(body, "input", rebuilt)
	if err != nil {
		return body, fmt.Errorf("replace patched native Codex input: %w", err)
	}
	return next, nil
}

// prepareOpenAICodexUpstreamIdentity 独立于可选的 prompt cache 转换执行 Codex 身份协议。
// 请求体字段分别按自己的原值改写；最终 session 头使用协议规定的优先级。
func prepareOpenAICodexUpstreamIdentity(
	c *gin.Context,
	account *Account,
	body []byte,
	compact bool,
) ([]byte, openAICodexUpstreamIdentity, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return body, openAICodexUpstreamIdentity{}, nil
	}
	identity, err := resolveOpenAICodexUpstreamIdentity(c, account, body, compact)
	if err != nil {
		return body, openAICodexUpstreamIdentity{}, err
	}
	if identity == (openAICodexUpstreamIdentity{}) {
		return body, identity, nil
	}
	updated, err := patchOpenAICodexIdentityBody(
		body,
		identity,
		openAICodexIdentityBodyOptions{Compact: compact},
	)
	if err != nil {
		return body, openAICodexUpstreamIdentity{}, err
	}
	if c != nil {
		c.Set(openAICodexUpstreamIdentityContextKey, identity)
	}
	return updated, identity, nil
}

// prepareOpenAICodexAuxiliaryResponsesIdentity 为无 session seed 的 Responses bridge 改写正文。
func prepareOpenAICodexAuxiliaryResponsesIdentity(
	c *gin.Context,
	account *Account,
	body []byte,
) ([]byte, openAICodexUpstreamIdentity, error) {
	identity, err := resolveOpenAICodexUpstreamIdentityWithSessionID(
		c,
		account,
		openAICodexInboundHeader(c, openAIOfficialSessionIDHeader),
	)
	if err != nil || identity == (openAICodexUpstreamIdentity{}) {
		return body, identity, err
	}
	updated, err := patchOpenAICodexIdentityBody(body, identity, openAICodexIdentityBodyOptions{})
	if err != nil {
		return body, openAICodexUpstreamIdentity{}, err
	}
	if c != nil {
		c.Set(openAICodexUpstreamIdentityContextKey, identity)
	}
	return updated, identity, nil
}

func resolveOpenAICodexUpstreamIdentity(
	c *gin.Context,
	account *Account,
	body []byte,
	compact bool,
) (openAICodexUpstreamIdentity, error) {
	rawSessionID := openAICodexRelaySessionSeedFromContext(c)
	if rawSessionID == "" {
		rawSessionID = stagedCodexFingerprintOriginalBodySessionID(c, account)
	}
	if rawSessionID == "" {
		rawSessionID = openAICodexRequestSessionSeed(c)
	}
	if rawSessionID == "" {
		rawSessionID = openAICodexRelaySessionSeed(c, body)
	}
	if rawSessionID == "" && compact {
		rawSessionID = resolveOpenAICompactSessionID(c)
	}
	return resolveOpenAICodexUpstreamIdentityWithSessionID(c, account, rawSessionID)
}

func openAICodexRequestSessionSeed(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	for _, name := range []string{
		openAIOfficialSessionIDHeader,
		openAIOfficialThreadIDHeader,
		"session_id",
		"conversation_id",
	} {
		if value := strings.TrimSpace(c.Request.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// openAICodexRelaySessionSeed 按官方协议优先级选择最终 session header 的原始种子。
func openAICodexRelaySessionSeed(c *gin.Context, body []byte) string {
	if sessionSeed := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.session_id").String()); sessionSeed != "" {
		return sessionSeed
	}
	if c != nil && c.Request != nil {
		for _, name := range []string{
			openAIOfficialSessionIDHeader,
			openAIOfficialThreadIDHeader,
			"session_id",
			"conversation_id",
		} {
			if value := strings.TrimSpace(c.Request.Header.Get(name)); value != "" {
				return value
			}
		}
	}
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
}

// resolveOpenAICodexUpstreamIdentityWithSessionID 使用调用方按端点协议确认的会话源。
func resolveOpenAICodexUpstreamIdentityWithSessionID(
	c *gin.Context,
	account *Account,
	rawSessionID string,
) (openAICodexUpstreamIdentity, error) {
	relayIdentity, ok := newOpenAICodexRelayIdentity(c, account)
	if !ok {
		return openAICodexUpstreamIdentity{}, nil
	}
	var err error
	previousIdentity, hasPreviousIdentity := openAICodexUpstreamIdentityFromContext(c)
	if hasPreviousIdentity && previousIdentity.RelayIdentity != relayIdentity {
		hasPreviousIdentity = false
	}

	rawThreadID := ""
	rawClientRequestID := ""
	if c != nil && c.Request != nil {
		rawThreadID = strings.TrimSpace(c.Request.Header.Get(openAIOfficialThreadIDHeader))
		rawClientRequestID = strings.TrimSpace(c.Request.Header.Get(openAIOfficialClientRequestIDHeader))
	}
	rawParentThreadID := ""
	if c != nil && c.Request != nil {
		rawParentThreadID = strings.TrimSpace(c.Request.Header.Get(openAICodexParentThreadIDHeader))
	}
	rawInstallationID := ""
	rawWindowID := ""
	if c != nil && c.Request != nil {
		rawInstallationID = strings.TrimSpace(c.Request.Header.Get("x-codex-installation-id"))
		rawWindowID = strings.TrimSpace(c.Request.Header.Get("x-codex-window-id"))
	}

	identity := openAICodexUpstreamIdentity{
		RelayIdentity:   relayIdentity,
		InstallationID:  relayIdentity.installationID(rawInstallationID),
		SessionID:       relayIdentity.pseudonymize("session_id", rawSessionID),
		ThreadID:        relayIdentity.pseudonymize("thread_id", rawThreadID),
		ClientRequestID: relayIdentity.pseudonymize("thread_id", rawClientRequestID),
		ParentThreadID:  relayIdentity.pseudonymize("thread_id", rawParentThreadID),
		WindowID:        relayIdentity.pseudonymize("window_id", rawWindowID),
	}
	// A Responses WebSocket session reuses one gin context across turns. Current
	// Codex clients may omit prompt_cache_key/client_metadata on follow-up
	// response.create frames, so carry forward the already-isolated identity
	// instead of silently dropping the cache/session headers after turn one.
	if hasPreviousIdentity {
		if identity.InstallationID == "" {
			identity.InstallationID = previousIdentity.InstallationID
		}
		if identity.SessionID == "" {
			identity.SessionID = previousIdentity.SessionID
		}
		if identity.ThreadID == "" {
			identity.ThreadID = previousIdentity.ThreadID
		}
		if identity.ClientRequestID == "" {
			identity.ClientRequestID = previousIdentity.ClientRequestID
		}
		if identity.ParentThreadID == "" {
			identity.ParentThreadID = previousIdentity.ParentThreadID
		}
		if identity.WindowID == "" {
			identity.WindowID = previousIdentity.WindowID
		}
	}
	if c != nil && c.Request != nil {
		identity.TurnState = strings.TrimSpace(c.Request.Header.Get(openAIWSTurnStateHeader))
		headerTurnMetadata := strings.TrimSpace(c.Request.Header.Get(openAIWSTurnMetadataHeader))
		if headerTurnMetadata != "" {
			identity.TurnMetadata, err = relayIdentity.rewriteTurnMetadata(headerTurnMetadata)
			if err != nil {
				return openAICodexUpstreamIdentity{}, err
			}
		}
	}
	return identity, nil
}

func patchOpenAICodexIdentityBody(
	body []byte,
	identity openAICodexUpstreamIdentity,
	options openAICodexIdentityBodyOptions,
) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body, fmt.Errorf("OpenAI Responses request body must be an object")
	}
	if options.Compact {
		return body, nil
	}

	updated := body
	var err error
	updated, err = rewriteOpenAICodexJSONBytesString(
		updated,
		"prompt_cache_key",
		"session_id",
		identity.RelayIdentity,
	)
	if err != nil {
		return body, err
	}

	clientMetadata := gjson.GetBytes(updated, "client_metadata")
	if clientMetadata.Exists() && !clientMetadata.IsObject() {
		return body, fmt.Errorf("codex client_metadata must be an object")
	}
	needsClientMetadata := identity.RelayIdentity.deviceID != "" || identity.TurnState != ""
	if !clientMetadata.Exists() && needsClientMetadata {
		updated, err = sjson.SetRawBytes(updated, "client_metadata", []byte(`{}`))
		if err != nil {
			return body, fmt.Errorf("create Codex client_metadata: %w", err)
		}
		clientMetadata = gjson.GetBytes(updated, "client_metadata")
	}
	if !clientMetadata.IsObject() {
		return updated, nil
	}

	for _, field := range []struct {
		name    string
		purpose string
	}{
		{name: "session_id", purpose: "session_id"},
		{name: "thread_id", purpose: "thread_id"},
		{name: "turn_id", purpose: "turn_id"},
		{name: "parent_thread_id", purpose: "thread_id"},
		{name: "parent_turn_id", purpose: "turn_id"},
		{name: "root_turn_id", purpose: "turn_id"},
		{name: openAIOfficialClientRequestIDHeader, purpose: "thread_id"},
		{name: "x-codex-window-id", purpose: "window_id"},
		{name: openAICodexParentThreadIDHeader, purpose: "thread_id"},
	} {
		path := "client_metadata." + field.name
		updated, err = rewriteOpenAICodexJSONBytesString(updated, path, field.purpose, identity.RelayIdentity)
		if err != nil {
			return body, err
		}
	}

	installationPath := "client_metadata.x-codex-installation-id"
	installationValue := gjson.GetBytes(updated, installationPath)
	clientInstallationID := ""
	if installationValue.Exists() && installationValue.Type != gjson.Null {
		if installationValue.Type != gjson.String {
			return body, fmt.Errorf("%s must be a string when provided", installationPath)
		}
		clientInstallationID = installationValue.String()
	}
	if installationID := identity.RelayIdentity.installationID(clientInstallationID); installationID != "" {
		updated, err = sjson.SetBytes(updated, installationPath, installationID)
		if err != nil {
			return body, fmt.Errorf("rewrite %s: %w", installationPath, err)
		}
	}

	turnMetadataPath := "client_metadata." + openAIWSTurnMetadataHeader
	turnMetadata := gjson.GetBytes(updated, turnMetadataPath)
	if turnMetadata.Exists() && turnMetadata.Type != gjson.Null {
		if turnMetadata.Type != gjson.String {
			return body, fmt.Errorf("%s must be a string when provided", turnMetadataPath)
		}
		rewritten, rewriteErr := identity.RelayIdentity.rewriteTurnMetadata(turnMetadata.String())
		if rewriteErr != nil {
			return body, rewriteErr
		}
		if rewritten != turnMetadata.String() {
			updated, err = sjson.SetBytes(updated, "client_metadata.x-codex-turn-metadata", rewritten)
			if err != nil {
				return body, fmt.Errorf("rewrite Codex body turn metadata: %w", err)
			}
		}
	}

	turnStatePath := "client_metadata." + openAIWSTurnStateHeader
	turnState := gjson.GetBytes(updated, turnStatePath)
	if turnState.Exists() && turnState.Type != gjson.Null && turnState.Type != gjson.String {
		return body, fmt.Errorf("%s must be a string when provided", turnStatePath)
	}
	if identity.TurnState != "" {
		updated, err = sjson.SetBytes(updated, turnStatePath, identity.TurnState)
		if err != nil {
			return body, fmt.Errorf("set current Codex turn state: %w", err)
		}
	}
	return updated, nil
}

func openAICodexUpstreamIdentityFromContext(c *gin.Context) (openAICodexUpstreamIdentity, bool) {
	if c == nil {
		return openAICodexUpstreamIdentity{}, false
	}
	value, ok := c.Get(openAICodexUpstreamIdentityContextKey)
	if !ok {
		return openAICodexUpstreamIdentity{}, false
	}
	identity, ok := value.(openAICodexUpstreamIdentity)
	if !ok || identity == (openAICodexUpstreamIdentity{}) {
		return openAICodexUpstreamIdentity{}, false
	}
	return identity, true
}

func applyOpenAICodexUpstreamIdentityHeaders(headers http.Header, identity openAICodexUpstreamIdentity) {
	if headers == nil || identity == (openAICodexUpstreamIdentity{}) {
		return
	}
	for _, name := range []string{
		"x-codex-installation-id",
		openAIOfficialSessionIDHeader,
		"session_id",
		openAIOfficialThreadIDHeader,
		openAIOfficialClientRequestIDHeader,
		openAICodexParentThreadIDHeader,
		"x-codex-window-id",
		openAIWSTurnMetadataHeader,
	} {
		headers.Del(name)
	}
	if identity.InstallationID != "" {
		headers.Set("x-codex-installation-id", identity.InstallationID)
	}
	if identity.SessionID != "" {
		setOpenAIUpstreamSessionHeaders(headers, identity.SessionID)
	}
	if identity.ThreadID != "" {
		headers.Set(openAIOfficialThreadIDHeader, identity.ThreadID)
	}
	if identity.ClientRequestID != "" {
		headers.Set(openAIOfficialClientRequestIDHeader, identity.ClientRequestID)
	}
	if identity.ParentThreadID != "" {
		headers.Set(openAICodexParentThreadIDHeader, identity.ParentThreadID)
	}
	if identity.WindowID != "" {
		headers.Set("x-codex-window-id", identity.WindowID)
	}
	if identity.TurnMetadata != "" {
		headers.Set(openAIWSTurnMetadataHeader, identity.TurnMetadata)
	}
}

// applyOpenAICodexAuxiliaryIdentityHeaders 保留辅助端点支持的官方身份头。
func applyOpenAICodexAuxiliaryIdentityHeaders(headers http.Header, identity openAICodexUpstreamIdentity) {
	applyOpenAICodexUpstreamIdentityHeaders(headers, identity)
	if headers == nil {
		return
	}
	headers.Del("session_id")
	headers.Del("conversation_id")
}
