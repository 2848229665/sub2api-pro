package service

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentModerationKeywordMatcherMatchesLegacyBehavior(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		keywords []string
	}{
		{name: "miss", text: "clean prompt", keywords: []string{"blocked", "secret"}},
		{name: "case insensitive", text: "contains SECRET value", keywords: []string{"secret"}},
		{name: "configured order wins", text: "early appears before later", keywords: []string{"later", "early"}},
		{name: "overlap uses configured order", text: "abc", keywords: []string{"bc", "abc"}},
		{name: "unicode", text: "这里包含敏感词和世界", keywords: []string{"世界", "敏感词"}},
		{name: "duplicates", text: "duplicate", keywords: []string{"duplicate", "DUPLICATE"}},
		{name: "empty entries", text: "blocked", keywords: []string{"", "blocked"}},
		{name: "kelvin sign lowers to ascii k", text: "leaKed data", keywords: []string{"leaked"}},
		{name: "fullwidth uppercase", text: "密码是ＡＢＣ123", keywords: []string{"ａｂｃ"}},
		{name: "accented uppercase", text: "CAFÉ MENU", keywords: []string{"café"}},
		{name: "mixed case unicode boundary", text: "前缀Éxploit后缀", keywords: []string{"éxploit"}},
		{name: "invalid utf8 replaced by replacement rune", text: "bad\xffbyte", keywords: []string{"bad�byte"}},
		{name: "invalid utf8 does not bridge match", text: "bad\xffbyte", keywords: []string{"badbyte"}},
		{name: "literal replacement rune in text", text: "has � marker", keywords: []string{"� marker"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantKeyword, wantHit := matchBlockedKeyword(tt.text, tt.keywords)
			gotKeyword, gotHit := newContentModerationKeywordMatcher(tt.keywords).Match(tt.text)
			require.Equal(t, wantHit, gotHit)
			require.Equal(t, wantKeyword, gotKeyword)
		})
	}
}

func TestContentModerationKeywordMatcherRandomizedParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20260714))
	const alphabet = "abcXYZ"
	for iteration := 0; iteration < 1000; iteration++ {
		keywords := make([]string, 1+rng.Intn(30))
		for index := range keywords {
			length := 1 + rng.Intn(8)
			var value strings.Builder
			for range length {
				_ = value.WriteByte(alphabet[rng.Intn(len(alphabet))])
			}
			keywords[index] = value.String()
		}
		var text strings.Builder
		for range 20 + rng.Intn(100) {
			_ = text.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}

		wantKeyword, wantHit := matchBlockedKeyword(text.String(), keywords)
		gotKeyword, gotHit := newContentModerationKeywordMatcher(keywords).Match(text.String())
		require.Equal(t, wantHit, gotHit, "iteration %d", iteration)
		require.Equal(t, wantKeyword, gotKeyword, "iteration %d", iteration)
	}
}

func TestContentModerationKeywordMatcherRandomizedUnicodeParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20260809))
	pieces := []string{
		"a", "B", "z", "0", " ",
		"世", "界", "敏", "感",
		"Ä", "é", "É", "ß",
		"K",            // Kelvin sign, lowers to ASCII k
		"Ａ",            // fullwidth A, lowers to fullwidth a
		"�",            // literal replacement rune
		"\xff", "\xc3", // invalid / truncated UTF-8 bytes
	}
	for iteration := 0; iteration < 500; iteration++ {
		keywords := make([]string, 1+rng.Intn(10))
		for index := range keywords {
			var value strings.Builder
			for range 1 + rng.Intn(4) {
				_, _ = value.WriteString(pieces[rng.Intn(len(pieces))])
			}
			keywords[index] = value.String()
		}
		var text strings.Builder
		for range 5 + rng.Intn(60) {
			_, _ = text.WriteString(pieces[rng.Intn(len(pieces))])
		}

		wantKeyword, wantHit := matchBlockedKeyword(text.String(), keywords)
		gotKeyword, gotHit := newContentModerationKeywordMatcher(keywords).Match(text.String())
		require.Equal(t, wantHit, gotHit, "iteration %d text %q keywords %q", iteration, text.String(), keywords)
		require.Equal(t, wantKeyword, gotKeyword, "iteration %d text %q keywords %q", iteration, text.String(), keywords)
	}
}
