package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// CyberSessionBlockStore 是 cyber 会话屏蔽表的存取接口。
// repository 层 gatewayCache 附带实现（类型断言探测接入，不改 GatewayCache
// 共享接口）；测试 stub 不实现时屏蔽能力自动降级关闭。
type CyberSessionBlockStore interface {
	SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error
	IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error)
	FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error)
}

// KeywordSessionBlockStore stores terminal local keyword-policy blocks in a
// namespace separate from cyber-policy state.
type KeywordSessionBlockStore interface {
	ClaimKeywordSessionBlocked(ctx context.Context, key string, ttl time.Duration) (bool, error)
	IsKeywordSessionBlocked(ctx context.Context, key string) (bool, error)
}

const policySessionWriteTimeout = 2 * time.Second

const cyberSessionTranscriptLookupOverflowBlockKey = "transcript_lookup_limit_exceeded"

// CyberSessionExplicitBlockKey returns an inexpensive exact key when the
// client supplies a stable session signal.
func CyberSessionExplicitBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	return hashCyberSessionBlockKey(apiKeyID, explicitOpenAISessionID(c, body))
}

// OpenAISessionBlockKey derives a policy-block key only from explicit session
// identifiers. It never falls back to API-key-wide or content-derived blocking.
func OpenAISessionBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	raw := explicitOpenAISessionID(c, body)
	if raw == "" {
		return ""
	}
	isolated := isolateOpenAISessionID(apiKeyID, raw)
	sum := sha256.Sum256([]byte(isolated))
	return hex.EncodeToString(sum[:])
}

// CyberSessionTranscriptBlockKeys returns the exact full-request key followed
// by an optional rewrite-tolerant context key. The latter is emitted only after
// model-generated history has been observed.
func CyberSessionTranscriptBlockKeys(apiKeyID int64, body []byte) []string {
	derived := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
	if len(derived.lookupKeys) == 0 {
		return nil
	}
	keys := []string{derived.lookupKeys[len(derived.lookupKeys)-1]}
	if derived.preLatestUserKey != "" && derived.preLatestUserKey != keys[0] {
		keys = append(keys, derived.preLatestUserKey)
	}
	return keys
}

func CyberSessionTranscriptLookupKeys(apiKeyID int64, body []byte) []string {
	return deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body).lookupKeys
}

// CyberSessionScopeKey is a coarse, non-blocking fingerprint used only to
// avoid transcript parsing and MGET for sources that never produced a hit.
func CyberSessionScopeKey(apiKeyID int64, clientIP, userAgent string) string {
	if apiKeyID <= 0 {
		return ""
	}
	raw := "cyber-scope:v1|api_key=" + strconv.FormatInt(apiKeyID, 10) +
		"|ip=" + strings.TrimSpace(clientIP) +
		"|ua=" + NormalizeSessionUserAgent(userAgent)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hashCyberSessionBlockKey(apiKeyID int64, raw string) string {
	if raw == "" {
		return ""
	}
	isolated := isolateOpenAISessionID(apiKeyID, raw)
	sum := sha256.Sum256([]byte(isolated))
	return hex.EncodeToString(sum[:])
}

func CyberSessionBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	return OpenAISessionBlockKey(apiKeyID, c, body)
}

// CyberPolicyBlockKey preserves explicit-session blocking when an identity is
// present. Stateless requests fall back to the normalized text of their latest
// user message, isolated by API key and protocol. Empty or non-textual inputs
// remain fail-open.
func CyberPolicyBlockKey(apiKeyID int64, c *gin.Context, protocol string, body []byte) string {
	if sessionKey := CyberSessionBlockKey(apiKeyID, c, body); sessionKey != "" {
		return sessionKey
	}
	text := ExtractLastUserMessageText(protocol, body)
	if text == "" {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("cyber-last-user:v1\x00"))
	_, _ = hash.Write([]byte(strconv.FormatInt(apiKeyID, 10)))
	_, _ = hash.Write([]byte{'\x00'})
	_, _ = hash.Write([]byte(protocol))
	_, _ = hash.Write([]byte{'\x00'})
	_, _ = hash.Write([]byte(text))
	return "message:v1:" + hex.EncodeToString(hash.Sum(nil))
}

func OpenAIKeywordSessionBlockKey(sessionKey, policyVersion string) string {
	if sessionKey == "" || policyVersion == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionKey + ":" + policyVersion))
	return hex.EncodeToString(sum[:])
}

// cyberSessionBlockStore 探测 cache 是否具备屏蔽存储能力。
// 注意：若未来以装饰器包装 GatewayCache（如日志/指标装饰器），该装饰器必须同时实现
// CyberSessionBlockStore，否则会话屏蔽能力将静默降级关闭
// （编译断言 var _ service.CyberSessionBlockStore = (*gatewayCache)(nil) 只覆盖
// *gatewayCache 本体，无法覆盖其外层包装）。
func (s *OpenAIGatewayService) cyberSessionBlockStore() CyberSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(CyberSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

func (s *OpenAIGatewayService) keywordSessionBlockStore() KeywordSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(KeywordSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

// CyberSessionBlockRuntime 返回 (开关, TTL)。开关默认关。
// 委托给 SettingService.GetCyberSessionBlockRuntime，进程内缓存避免热路径 DB 往返。
func (s *OpenAIGatewayService) CyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settingService == nil {
		return false, time.Hour
	}
	return s.settingService.GetCyberSessionBlockRuntime(ctx)
}

// MarkCyberSessionBlocked 把会话写入屏蔽表（写入点：cyber 命中后）。
// 开关关闭、key 为空或存储不可用时静默跳过。
func (s *OpenAIGatewayService) MarkCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	enabled, ttl := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return
	}
	if err := store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block write failed: err=%v", err)
	}
}

func (s *OpenAIGatewayService) IsCyberSessionBlocked(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return false
	}
	blocked, err := store.FindCyberSessionBlocked(ctx, []string{key})
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block read failed: err=%v", err)
		return false
	}
	return blocked == key
}

// FindCyberSessionBlockedForRequest applies explicit-first lookup followed by
// scope-gated transcript matching. All failures remain fail-open.
func (s *OpenAIGatewayService) FindCyberSessionBlockedForRequest(ctx context.Context, apiKeyID int64, c *gin.Context, body []byte, clientIP, userAgent string) string {
	enabled, _ := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return ""
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return ""
	}
	if explicitKey := CyberSessionExplicitBlockKey(apiKeyID, c, body); explicitKey != "" {
		key, err := store.FindCyberSessionBlocked(ctx, []string{explicitKey})
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "cyber explicit session read failed: err=%v", err)
			return ""
		}
		if key != "" {
			return key
		}
	}
	scopeKey := CyberSessionScopeKey(apiKeyID, clientIP, userAgent)
	active, err := store.IsCyberSessionScopeActive(ctx, scopeKey)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session scope read failed: err=%v", err)
		return ""
	}
	transcript := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
	if !active && len(transcript.lookupKeys) == 0 {
		return ""
	}
	if transcript.lookupKeysTruncated {
		if !active {
			return ""
		}
		// Once the coarse scope is active, silently dropping old candidates would
		// let a blocked client evade prefix matching by appending dummy items.
		return cyberSessionTranscriptLookupOverflowBlockKey
	}
	keys := transcript.lookupKeys
	if len(keys) == 0 {
		return ""
	}
	key, err := store.FindCyberSessionBlocked(ctx, keys)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block batch read failed: err=%v", err)
		return ""
	}
	return key
}

func (s *OpenAIGatewayService) IsKeywordSessionBlocked(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	store := s.keywordSessionBlockStore()
	if store == nil {
		return false
	}
	blocked, err := store.IsKeywordSessionBlocked(ctx, key)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "keyword session block read failed: err=%v", err)
		return false
	}
	return blocked
}

// ClaimKeywordSessionBlocked atomically creates a terminal block before the
// winning request records its local keyword hit. A false result means another
// request already claimed the same policy-scoped session.
func (s *OpenAIGatewayService) ClaimKeywordSessionBlocked(ctx context.Context, key string) (claimed bool, available bool) {
	if key == "" {
		return false, false
	}
	store := s.keywordSessionBlockStore()
	if store == nil {
		return false, false
	}
	_, ttl := s.CyberSessionBlockRuntime(ctx)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policySessionWriteTimeout)
	defer cancel()
	claimed, err := store.ClaimKeywordSessionBlocked(persistCtx, key, ttl)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "keyword session block claim failed: err=%v", err)
		return false, false
	}
	return claimed, true
}
