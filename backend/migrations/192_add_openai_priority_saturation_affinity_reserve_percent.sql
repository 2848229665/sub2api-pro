-- Store the global share of each finite OpenAI account's concurrency reserved
-- for session_hash and previous_response_id affinity.
INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_affinity_reserve_percent', '20', NOW())
ON CONFLICT (key) DO NOTHING;
