package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAICodexRelayIdentity 将客户端身份限定在下游 API Key 与上游账号范围内。
type openAICodexRelayIdentity struct {
	apiKeyHash string
	accountID  string
	deviceID   string
}

// openAICodexInboundHeader 读取并清理客户端提供的 Codex 请求头。
func openAICodexInboundHeader(c *gin.Context, name string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetHeader(name))
}

// newOpenAICodexRelayIdentity 从已完成鉴权的请求上下文构造可信身份。
func newOpenAICodexRelayIdentity(c *gin.Context, account *Account) (openAICodexRelayIdentity, bool) {
	return newOpenAICodexRelayIdentityForAPIKey(getAPIKeyFromContext(c), account)
}

// newOpenAICodexRelayIdentityForAPIKey 使用已鉴权 API Key 与已选账号构造身份。
func newOpenAICodexRelayIdentityForAPIKey(apiKey *APIKey, account *Account) (openAICodexRelayIdentity, bool) {
	if account == nil || !account.IsOpenAIOAuth() {
		return openAICodexRelayIdentity{}, false
	}
	if apiKey == nil {
		return openAICodexRelayIdentity{}, false
	}
	apiKeyValue := strings.TrimSpace(apiKey.Key)
	if apiKeyValue == "" {
		return openAICodexRelayIdentity{}, false
	}

	digest := sha256.Sum256([]byte(apiKeyValue))
	return openAICodexRelayIdentity{
		apiKeyHash: hex.EncodeToString(digest[:]),
		accountID:  strconv.FormatInt(account.ID, 10),
		deviceID:   strings.TrimSpace(account.GetOpenAIDeviceID()),
	}, true
}

// pseudonymize 生成账号、API Key 与字段用途隔离的稳定 UUID。
func (i openAICodexRelayIdentity) pseudonymize(purpose, raw string) string {
	purpose = strings.TrimSpace(purpose)
	raw = strings.TrimSpace(raw)
	if i.apiKeyHash == "" || i.accountID == "" || purpose == "" || raw == "" {
		return ""
	}

	mac := hmac.New(sha256.New, []byte(i.apiKeyHash))
	writeOpenAICodexIdentityPart(mac, []byte(i.accountID))
	writeOpenAICodexIdentityPart(mac, []byte(purpose))
	writeOpenAICodexIdentityPart(mac, []byte(raw))
	digest := mac.Sum(nil)

	var id uuid.UUID
	copy(id[:], digest[:16])
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

// installationID 优先使用账号固定设备，否则隔离客户端提供的 installation ID。
func (i openAICodexRelayIdentity) installationID(clientInstallationID string) string {
	if i.deviceID != "" {
		return i.deviceID
	}
	return i.pseudonymize("installation_id", clientInstallationID)
}

// rewriteTurnMetadata 改写 turn metadata 中的身份字段并移除工作区信息。
func (i openAICodexRelayIdentity) rewriteTurnMetadata(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("x-codex-turn-metadata must be valid JSON")
	}
	if !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return "", fmt.Errorf("x-codex-turn-metadata must be a JSON object")
	}

	updated := raw
	var err error
	for _, field := range []struct {
		name    string
		purpose string
	}{
		{name: "session_id", purpose: "session_id"},
		{name: "thread_id", purpose: "thread_id"},
		{name: "turn_id", purpose: "turn_id"},
		{name: "window_id", purpose: "window_id"},
		{name: "forked_from_thread_id", purpose: "thread_id"},
		{name: "parent_thread_id", purpose: "thread_id"},
		{name: "parent_turn_id", purpose: "turn_id"},
		{name: "root_turn_id", purpose: "turn_id"},
	} {
		updated, err = rewriteOpenAICodexJSONString(updated, field.name, field.purpose, i)
		if err != nil {
			return "", err
		}
	}

	installation := gjson.Get(updated, "installation_id")
	clientInstallationID := ""
	if installation.Exists() && installation.Type != gjson.Null {
		if installation.Type != gjson.String {
			return "", fmt.Errorf("installation_id must be a string when provided")
		}
		clientInstallationID = installation.String()
	}
	if value := i.installationID(clientInstallationID); value != "" {
		updated, err = sjson.Set(updated, "installation_id", value)
		if err != nil {
			return "", fmt.Errorf("rewrite installation_id: %w", err)
		}
	}
	if gjson.Get(updated, "workspaces").Exists() {
		updated, err = sjson.Delete(updated, "workspaces")
		if err != nil {
			return "", fmt.Errorf("remove workspaces from x-codex-turn-metadata: %w", err)
		}
	}
	return updated, nil
}

// rewriteOpenAICodexJSONString 对 JSON 字符串中的一个可选身份字段执行隔离。
func rewriteOpenAICodexJSONString(raw, path, purpose string, identity openAICodexRelayIdentity) (string, error) {
	value := gjson.Get(raw, path)
	if !value.Exists() || value.Type == gjson.Null {
		return raw, nil
	}
	if value.Type != gjson.String {
		return "", fmt.Errorf("%s must be a string when provided", path)
	}
	rewritten := identity.pseudonymize(purpose, value.String())
	if rewritten == "" {
		return raw, nil
	}
	updated, err := sjson.Set(raw, path, rewritten)
	if err != nil {
		return "", fmt.Errorf("rewrite %s: %w", path, err)
	}
	return updated, nil
}

// rewriteOpenAICodexJSONBytesString 对请求体中的一个可选字符串身份字段执行隔离。
func rewriteOpenAICodexJSONBytesString(body []byte, path, purpose string, identity openAICodexRelayIdentity) ([]byte, error) {
	value := gjson.GetBytes(body, path)
	if !value.Exists() || value.Type == gjson.Null {
		return body, nil
	}
	if value.Type != gjson.String {
		return nil, fmt.Errorf("%s must be a string when provided", path)
	}
	rewritten := identity.pseudonymize(purpose, value.String())
	if rewritten == "" {
		return body, nil
	}
	updated, err := sjson.SetBytes(body, path, rewritten)
	if err != nil {
		return nil, fmt.Errorf("rewrite %s: %w", path, err)
	}
	return updated, nil
}

// writeOpenAICodexIdentityPart 写入无歧义的 8 字节大端长度前缀与字段内容。
func writeOpenAICodexIdentityPart(mac interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write(value)
}
