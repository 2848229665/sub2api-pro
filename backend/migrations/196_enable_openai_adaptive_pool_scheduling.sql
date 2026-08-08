-- Enable the account-primary / API-key-elastic OpenAI pool scheduler for
-- installations that have not explicitly stored this setting yet. Existing
-- administrator choices are preserved by ON CONFLICT DO NOTHING.
INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_pool_balance_enabled', 'true', NOW())
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES (
    'openai_priority_saturation_account_share_percent',
    (
        100 - COALESCE(
            (
                SELECT CASE
                    WHEN BTRIM(value) ~ '^[0-9]{1,2}$'
                        AND BTRIM(value)::int BETWEEN 1 AND 99
                        THEN BTRIM(value)::int
                    ELSE 33
                END
                FROM settings
                WHERE key = 'openai_priority_saturation_api_key_share_percent'
                LIMIT 1
            ),
            33
        )
    )::text,
    NOW()
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_api_key_share_percent', '33', NOW())
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_enter_high_load_percent', '70', NOW())
ON CONFLICT (key) DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_exit_high_load_percent', '50', NOW())
ON CONFLICT (key) DO NOTHING;
