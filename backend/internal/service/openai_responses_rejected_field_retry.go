package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAIResponsesRectifierEnabled 读取 /v1/responses 请求整流器开关：
// 管理端运行时设置优先，未接入 settingService 时回退到 YAML/环境变量配置。
func (s *OpenAIGatewayService) openAIResponsesRectifierEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		return s.settingService.IsOpenAIResponsesRectifierEnabled(ctx)
	}
	return s.cfg != nil && s.cfg.Gateway.OpenAIResponsesRectifierEnabled
}

const maxOpenAIResponsesRejectedFieldRetries = 6

var (
	openAIResponsesRejectedIndexedParamPattern  = regexp.MustCompile(`(?i)^input\[(\d+)\]\.(namespace|item_reference)$`)
	openAIResponsesRejectedMessageParamPattern  = regexp.MustCompile(`(?i)(?:unknown|unsupported)[ _-]+parameter\s*(?::|=|is)?\s*["']?(max_output_tokens|reasoning_effort|background|reasoning\.summary|input\[\d+\]\.(?:namespace|item_reference))(?:["']|\b)`)
	openAIResponsesContentTooLongMessagePattern = regexp.MustCompile(`(?i)invalid '?input\[(\d+)\]\.content'?:\s*array too long\.\s*expected an array with maximum length 0`)
)

// openAIResponsesRejectedTopLevelParams maps explicitly rejected
// unknown/unsupported parameters to the body path deleted before the bounded
// retry. Values are gjson/sjson paths, so "reasoning.summary" targets the
// nested reasoning object field.
var openAIResponsesRejectedTopLevelParams = map[string]string{
	"max_output_tokens": "max_output_tokens",
	"reasoning_effort":  "reasoning_effort",
	"background":        "background",
	"reasoning.summary": "reasoning.summary",
}

type openAIResponsesRejectedFieldRetryState struct {
	attempts       int
	seenBodyHashes map[[sha256.Size]byte]struct{}
}

func newOpenAIResponsesRejectedFieldRetryState(initialBody []byte) *openAIResponsesRejectedFieldRetryState {
	state := &openAIResponsesRejectedFieldRetryState{
		seenBodyHashes: make(map[[sha256.Size]byte]struct{}, maxOpenAIResponsesRejectedFieldRetries+1),
	}
	state.remember(initialBody)
	return state
}

func (s *openAIResponsesRejectedFieldRetryState) Allow(nextBody []byte) bool {
	if s == nil || len(nextBody) == 0 || s.attempts >= maxOpenAIResponsesRejectedFieldRetries {
		return false
	}
	bodyHash := sha256.Sum256(nextBody)
	if _, seen := s.seenBodyHashes[bodyHash]; seen {
		return false
	}
	s.seenBodyHashes[bodyHash] = struct{}{}
	s.attempts++
	return true
}

func (s *openAIResponsesRejectedFieldRetryState) remember(body []byte) {
	if s == nil || len(body) == 0 {
		return
	}
	if s.seenBodyHashes == nil {
		s.seenBodyHashes = make(map[[sha256.Size]byte]struct{}, maxOpenAIResponsesRejectedFieldRetries+1)
	}
	s.seenBodyHashes[sha256.Sum256(body)] = struct{}{}
}

// normalizeOpenAIResponsesRejectedFieldRetryBody rewrites the request body
// after an explicit upstream 400 field rejection. extended=false restores the
// historical behavior (max_output_tokens and input[N].namespace only); the
// additional rules are gated by the responses-rectifier switch.
func normalizeOpenAIResponsesRejectedFieldRetryBody(statusCode int, body, responseBody []byte, extended bool) ([]byte, string, bool, error) {
	if statusCode != http.StatusBadRequest || len(body) == 0 || len(responseBody) == 0 {
		return nil, "", false, nil
	}

	code := strings.ToLower(strings.TrimSpace(extractUpstreamErrorCode(responseBody)))
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
	if isExplicitOpenAIResponsesFieldRejection(code, message) {
		param := strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, "error.param").String()))
		if param == "" {
			param = openAIResponsesRejectedParamFromMessage(message)
		}
		if index, field, ok := openAIResponsesRejectedIndexedParam(param); ok {
			switch field {
			case "namespace":
				return removeOpenAIResponsesRejectedNamespaceAtIndex(body, index)
			case "item_reference":
				if extended {
					return removeOpenAIResponsesRejectedItemReferenceAtIndex(body, index)
				}
			}
			return nil, "", false, nil
		}
		if !extended && param != "max_output_tokens" {
			return nil, "", false, nil
		}
		if path, ok := openAIResponsesRejectedTopLevelParams[param]; ok && gjson.GetBytes(body, path).Exists() {
			retryBody, err := sjson.DeleteBytes(body, path)
			if err != nil {
				return nil, "", false, fmt.Errorf("delete rejected %s: %w", path, err)
			}
			return retryBody, path + " parameter rejection", true, nil
		}
		return nil, "", false, nil
	}
	if extended {
		if index, ok := openAIResponsesContentTooLongIndex(message); ok {
			return emptyOpenAIResponsesRejectedContentAtIndex(body, index)
		}
	}
	return nil, "", false, nil
}

func isExplicitOpenAIResponsesFieldRejection(code, message string) bool {
	switch strings.TrimSpace(code) {
	case "unknown_parameter", "unsupported_parameter":
		return true
	}
	return strings.Contains(message, "unknown parameter") ||
		strings.Contains(message, "unsupported parameter")
}

func openAIResponsesRejectedParamFromMessage(message string) string {
	match := openAIResponsesRejectedMessageParamPattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

func openAIResponsesRejectedIndexedParam(param string) (int, string, bool) {
	match := openAIResponsesRejectedIndexedParamPattern.FindStringSubmatch(strings.TrimSpace(param))
	if len(match) != 3 {
		return 0, "", false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index < 0 {
		return 0, "", false
	}
	return index, strings.ToLower(match[2]), true
}

func openAIResponsesContentTooLongIndex(message string) (int, bool) {
	match := openAIResponsesContentTooLongMessagePattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return 0, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func removeOpenAIResponsesRejectedNamespaceAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, itemPath+".type").String()))
	switch itemType {
	case "function_call", "tool_call", "custom_tool_call", "mcp_tool_call":
	default:
		return nil, "", false, nil
	}

	namespacePath := itemPath + ".namespace"
	if !gjson.GetBytes(body, namespacePath).Exists() {
		return nil, "", false, nil
	}
	retryBody, err := sjson.DeleteBytes(body, namespacePath)
	if err != nil {
		return nil, "", false, fmt.Errorf("delete rejected namespace at input[%d]: %w", index, err)
	}
	return retryBody, "indexed namespace parameter rejection", true, nil
}

func removeOpenAIResponsesRejectedItemReferenceAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	item := gjson.GetBytes(body, itemPath)
	if !item.Exists() {
		return nil, "", false, nil
	}
	if item.Get("item_reference").Exists() {
		retryBody, err := sjson.DeleteBytes(body, itemPath+".item_reference")
		if err != nil {
			return nil, "", false, fmt.Errorf("delete rejected item_reference at input[%d]: %w", index, err)
		}
		return retryBody, "indexed item_reference parameter rejection", true, nil
	}
	if strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "item_reference") {
		retryBody, err := sjson.DeleteBytes(body, itemPath)
		if err != nil {
			return nil, "", false, fmt.Errorf("remove rejected item_reference item at input[%d]: %w", index, err)
		}
		return retryBody, "indexed item_reference item rejection", true, nil
	}
	return nil, "", false, nil
}

// emptyOpenAIResponsesRejectedContentAtIndex rewrites input[N].content to an
// empty array when the upstream rejected it with "array too long. Expected an
// array with maximum length 0" (a codex-spark history-item constraint).
func emptyOpenAIResponsesRejectedContentAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	contentPath := fmt.Sprintf("input.%d.content", index)
	content := gjson.GetBytes(body, contentPath)
	if !content.IsArray() || len(content.Array()) == 0 {
		return nil, "", false, nil
	}
	retryBody, err := sjson.SetRawBytes(body, contentPath, []byte("[]"))
	if err != nil {
		return nil, "", false, fmt.Errorf("empty rejected content at input[%d]: %w", index, err)
	}
	return retryBody, "indexed content max-length rejection", true, nil
}
