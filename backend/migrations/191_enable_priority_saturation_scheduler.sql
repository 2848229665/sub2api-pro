-- Enable deterministic priority-saturation scheduling by default. The
-- weighted-topk switch is independent and may remain enabled; priority
-- saturation takes precedence while its switch is on.
INSERT INTO settings (key, value, updated_at)
VALUES ('openai_priority_saturation_enabled', 'true', NOW())
ON CONFLICT (key) DO NOTHING;
