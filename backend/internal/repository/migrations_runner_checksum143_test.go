package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// The fork reworded migration 143's header comment in place (no schema change),
// which changes its checksum. Without a compatibility rule any database that
// applied the upstream wording aborts on the next startup migration, which also
// blocks every later fork migration. This test locks the rule to the real
// embedded file so a wrong/stale fileChecksum, or a later edit to 143, is
// caught before it ships.
func TestMigration143ChecksumCompatibilityMatchesEmbeddedFile(t *testing.T) {
	const name = "143_group_models_list_config.sql"
	// Historical upstream wording checksum a live database may still store.
	const upstreamChecksum = "b0a2cac2567db903a8967456fff348f59530c2633b4dae363c32a0e3b6503cb3"

	content, err := fs.ReadFile(migrations.FS, name)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
	fileChecksum := hex.EncodeToString(sum[:])

	rule, ok := migrationChecksumCompatibilityRules[name]
	require.True(t, ok, "migration 143 must have a checksum compatibility rule")
	require.Equal(t, fileChecksum, rule.fileChecksum,
		"143 rule fileChecksum must match the current embedded migration content")
	require.NotEqual(t, upstreamChecksum, fileChecksum,
		"the fork reworded 143, so its checksum must differ from the upstream wording")

	// Upgrade path: the database stored the upstream checksum while the file is
	// the fork version. This is the case the rule exists to unblock.
	require.True(t, isMigrationChecksumCompatible(name, upstreamChecksum, fileChecksum),
		"a DB on the upstream 143 checksum must accept the fork file on upgrade")
	// Already upgraded: the database and file both carry the fork checksum.
	require.True(t, isMigrationChecksumCompatible(name, fileChecksum, fileChecksum))
	// An unrelated/unknown stored checksum is still rejected.
	require.False(t, isMigrationChecksumCompatible(name, strings.Repeat("0", 64), fileChecksum))
}
