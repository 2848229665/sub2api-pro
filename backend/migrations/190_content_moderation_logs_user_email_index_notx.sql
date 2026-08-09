-- Speed up content moderation log search by user email.
-- The admin panel frequently filters logs by exact email address;
-- without this index the query scans 30k+ rows and times out.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_content_moderation_logs_user_email
    ON content_moderation_logs (user_email, created_at DESC);
